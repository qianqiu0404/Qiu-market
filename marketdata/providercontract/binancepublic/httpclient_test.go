package binancepublic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

func TestProductionClientPinsSecurityPolicy(t *testing.T) {
	client, err := newProductionClient(clientOptions{})
	require.NoError(t, err)
	require.Equal(t, productionOrigin, client.origin.String())
	require.Nil(t, client.httpClient.Jar)
	require.Equal(t, requestTimeout, client.httpClient.Timeout)
	require.ErrorIs(t, client.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	transport := client.httpClient.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy, "the provider client must not read proxy environment variables")
	require.NotNil(t, transport.TLSClientConfig)
	require.GreaterOrEqual(t, transport.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
	require.False(t, transport.TLSClientConfig.InsecureSkipVerify)
}

func TestTestOriginAndTLSOverridesAreStrictlyLoopbackAndVerified(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	for _, raw := range []string{
		"http://127.0.0.1:8443",
		"https://localhost:8443",
		"https://192.0.2.1:8443",
		"https://user:password@127.0.0.1:8443",
		"https://127.0.0.1:8443/path",
		"https://127.0.0.1:8443?query=1",
		"https://127.0.0.1:8443#fragment",
		productionOrigin,
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := newTestClient(raw, transport, clientOptions{})
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
			})
		})
	}
	insecure := transport.Clone()
	insecure.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test rejection only
	_, err := newTestClient("https://127.0.0.1:8443", insecure, clientOptions{})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
}

func TestDoJSONRejectsEverythingOutsideExactOperationAllowlist(t *testing.T) {
	hits := 0
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}), providercontract.NewManualClock(time.Now()), nil)

	tests := []struct {
		name      string
		operation string
		path      string
		query     url.Values
		dst       any
	}{
		{"unknown operation", "raw", tickerPath, tickerQuery(), &map[string]any{}},
		{"full URL path", OperationTicker24h, "https://example.com/", tickerQuery(), &map[string]any{}},
		{"wrong ticker path", OperationTicker24h, klinesPath, tickerQuery(), &map[string]any{}},
		{"missing ticker query", OperationTicker24h, tickerPath, url.Values{"symbol": {"BTCUSDT"}}, &map[string]any{}},
		{"duplicate ticker value", OperationTicker24h, tickerPath, url.Values{
			"symbol": {"BTCUSDT", "ETHUSDT"}, "type": {"FULL"}, "symbolStatus": {"TRADING"},
		}, &map[string]any{}},
		{"extra ticker query", OperationTicker24h, tickerPath, func() url.Values {
			value := tickerQuery()
			value.Set("redirect", "https://example.com")
			return value
		}(), &map[string]any{}},
		{"wrong kline interval", OperationKlines, klinesPath, url.Values{
			"symbol": {"BTCUSDT"}, "interval": {"5m"}, "limit": {"10"}, "timeZone": {"0"},
		}, &[][]any{}},
		{"wrong kline limit", OperationKlines, klinesPath, url.Values{
			"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"1000"}, "timeZone": {"0"},
		}, &[][]any{}},
		{"wrong kline timezone", OperationKlines, klinesPath, url.Values{
			"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"10"}, "timeZone": {"8"},
		}, &[][]any{}},
		{"nil destination", OperationTicker24h, tickerPath, tickerQuery(), nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.DoJSON(context.Background(), test.operation, test.path, test.query, test.dst)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
			})
		})
	}
	require.Zero(t, hits)
}

func TestDoJSONMapsHTTPStatusesToTypedErrors(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		status       int
		kind         providercontract.ErrorKind
		retryAfter   string
		wantRetry    time.Duration
		wantFallback bool
	}{
		{http.StatusBadRequest, providercontract.ErrorBadRequest, "", 0, false},
		{http.StatusUnauthorized, providercontract.ErrorAuth, "", 0, false},
		{http.StatusForbidden, providercontract.ErrorPermission, "", 0, false},
		{http.StatusUnavailableForLegalReasons, providercontract.ErrorPermission, "", 0, false},
		{http.StatusRequestTimeout, providercontract.ErrorTimeout, "", 0, true},
		{http.StatusTeapot, providercontract.ErrorRateLimit, "12", 12 * time.Second, true},
		{http.StatusTooManyRequests, providercontract.ErrorRateLimit, "12", 12 * time.Second, true},
		{http.StatusInternalServerError, providercontract.ErrorUpstream5xx, "", 0, true},
		{http.StatusServiceUnavailable, providercontract.ErrorUpstream5xx, "", 0, true},
		{http.StatusTemporaryRedirect, providercontract.ErrorBadPayload, "", 0, false},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			recorder := &observationRecorder{}
			client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`provider error body must not escape`))
			}), providercontract.NewManualClock(now), recorder)
			var dst map[string]any
			_, err := client.DoJSON(context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &dst)
			require.ErrorIs(t, err, &providercontract.ProviderError{Kind: test.kind, Provider: ProviderID})
			require.Equal(t, test.wantFallback, providercontract.FallbackEligible(err))
			var typedError *providercontract.ProviderError
			require.ErrorAs(t, err, &typedError)
			require.Equal(t, test.wantRetry, typedError.RetryAfter)
			require.NotContains(t, err.Error(), "provider error body")
			values := recorder.Values()
			require.Len(t, values, 1)
			require.Equal(t, test.kind, values[0].ErrorKind)
			require.Equal(t, test.status, values[0].StatusCode)
			require.Equal(t, test.wantRetry, values[0].RetryAfter)
		})
	}
}

func TestRetryAfterSupportsDeltaDateAndDeterministicFallback(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"delta", "17", 17 * time.Second},
		{"date", now.Add(45 * time.Second).Format(http.TimeFormat), 45 * time.Second},
		{"missing", "", defaultRetryWait},
		{"invalid", "tomorrow", defaultRetryWait},
		{"past", now.Add(-time.Hour).Format(http.TimeFormat), defaultRetryWait},
		{"bounded", "999999999999999999", maximumRetryWait},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseRetryAfter(test.header, now)
			require.Equal(t, test.want, got)
		})
	}
}

func TestDoJSONRejectsWrongMediaMalformedTrailingAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"wrong media", "text/html", `{}`},
		{"missing media", "", `{}`},
		{"malformed", "application/json", `{"symbol":`},
		{"trailing JSON", "application/json", `{} {}`},
		{"oversized", "application/json", `{"padding":"` + strings.Repeat("x", tickerBodyLimit) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.contentType != "" {
					w.Header().Set("Content-Type", test.contentType)
				}
				_, _ = w.Write([]byte(test.body))
			}), providercontract.NewManualClock(time.Now()), nil)
			dst := map[string]any{"sentinel": "unchanged"}
			_, err := client.DoJSON(context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &dst)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
			require.Equal(t, map[string]any{"sentinel": "unchanged"}, dst)
		})
	}
}

func TestDoJSONNeverFollowsRedirects(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}), providercontract.NewManualClock(time.Now()), nil)

	var dst map[string]any
	_, err := client.DoJSON(context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &dst)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
	})
	require.Zero(t, targetHits)
}

func TestDoJSONClassifiesTimeoutCancellationNetworkAndTLSIdentity(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}), providercontract.NewManualClock(time.Now()), nil)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		var dst map[string]any
		_, err := client.DoJSON(ctx, OperationTicker24h, tickerPath, tickerQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorTimeout, Provider: ProviderID,
		})
	})

	t.Run("caller cancellation", func(t *testing.T) {
		client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}), providercontract.NewManualClock(time.Now()), nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var dst map[string]any
		_, err := client.DoJSON(ctx, OperationTicker24h, tickerPath, tickerQuery(), &dst)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("network", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		origin := "https://" + listener.Addr().String()
		require.NoError(t, listener.Close())
		transport := http.DefaultTransport.(*http.Transport).Clone()
		client, err := newTestClient(origin, transport, clientOptions{})
		require.NoError(t, err)
		var dst map[string]any
		_, err = client.DoJSON(context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorNetwork, Provider: ProviderID,
		})
	})

	t.Run("TLS identity", func(t *testing.T) {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
		server.StartTLS()
		defer server.Close()
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool(),
		}
		client, err := newTestClient(server.URL, transport, clientOptions{})
		require.NoError(t, err)
		var dst map[string]any
		_, err = client.DoJSON(context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorInvalidIdentity, Provider: ProviderID,
		})
	})
}

func TestObservationIsRecordedExactlyOnceForRejectedCall(t *testing.T) {
	recorder := &observationRecorder{}
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("rejected request reached network")
	}), providercontract.NewManualClock(time.Now()), recorder)
	var dst map[string]any
	_, err := client.DoJSON(context.Background(), "attacker-controlled-operation", "/", nil, &dst)
	require.Error(t, err)
	require.Equal(t, []Observation{{
		Provider: ProviderID, Operation: "invalid", Outcome: "error",
		ErrorKind: providercontract.ErrorBadRequest,
	}}, recorder.Values())
}
