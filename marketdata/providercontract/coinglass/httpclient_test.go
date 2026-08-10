package coinglass

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
	client, err := newProductionClient(clientOptions{SecretProvider: fixedSecretProvider("test-only")})
	require.NoError(t, err)
	require.Equal(t, productionOrigin, client.origin.String())
	require.Nil(t, client.httpClient.Jar)
	require.Equal(t, requestTimeout, client.httpClient.Timeout)
	require.ErrorIs(t, client.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	transport := client.httpClient.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy, "provider transport must not read proxy environment variables")
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
	insecure.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // rejection test only
	_, err := newTestClient("https://127.0.0.1:8443", insecure, clientOptions{})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
}

func TestDoJSONRejectsEverythingOutsideExactOperationAllowlistBeforeSecretOrNetwork(t *testing.T) {
	hits := 0
	secretReads := 0
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}), providercontract.NewManualClock(time.Now()), nil, secretProviderFunc(func(context.Context) ([]byte, error) {
		secretReads++
		return []byte("not-used"), nil
	}))

	tests := []struct {
		name      string
		operation string
		path      string
		query     url.Values
		dst       any
	}{
		{"unknown operation", "raw", openInterestHistoryPath, openInterestQuery(), &fixtureEnvelope{}},
		{"full URL path", OperationOpenInterestHistory, "https://example.com/", openInterestQuery(), &fixtureEnvelope{}},
		{"wrong OI path", OperationOpenInterestHistory, liquidationHistoryPath, openInterestQuery(), &fixtureEnvelope{}},
		{"wrong OI symbol", OperationOpenInterestHistory, openInterestHistoryPath, func() url.Values {
			value := openInterestQuery()
			value.Set("symbol", "BTCUSDT")
			return value
		}(), &fixtureEnvelope{}},
		{"wrong OI interval", OperationOpenInterestHistory, openInterestHistoryPath, func() url.Values {
			value := openInterestQuery()
			value.Set("interval", "1m")
			return value
		}(), &fixtureEnvelope{}},
		{"wrong OI unit", OperationOpenInterestHistory, openInterestHistoryPath, func() url.Values {
			value := openInterestQuery()
			value.Set("unit", "coin")
			return value
		}(), &fixtureEnvelope{}},
		{"extra query", OperationLiquidationHistory, liquidationHistoryPath, func() url.Values {
			value := liquidationQuery()
			value.Set("redirect", "https://example.com")
			return value
		}(), &fixtureEnvelope{}},
		{"duplicate query value", OperationLiquidationHistory, liquidationHistoryPath, func() url.Values {
			value := liquidationQuery()
			value["symbol"] = []string{"BTCUSD_PERP", "ETHUSD_PERP"}
			return value
		}(), &fixtureEnvelope{}},
		{"nil destination", OperationLiquidationHistory, liquidationHistoryPath, liquidationQuery(), nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.DoJSON(context.Background(), test.operation, test.path, test.query, test.dst)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
			})
		})
	}
	require.Zero(t, secretReads)
	require.Zero(t, hits)
}

func TestMissingInvalidAndFailingSecretsNeverReachNetworkAndReturnedCopiesAreCleared(t *testing.T) {
	tests := []struct {
		name     string
		provider SecretProvider
		kind     providercontract.ErrorKind
		raw      []byte
	}{
		{name: "missing provider", kind: providercontract.ErrorUnconfigured},
		{name: "provider error", provider: secretProviderFunc(func(context.Context) ([]byte, error) {
			return nil, errors.New("unit-test-secret-sentinel must not escape")
		}), kind: providercontract.ErrorAuth},
		{name: "empty", raw: []byte{}, kind: providercontract.ErrorAuth},
		{name: "whitespace", raw: []byte("unit test secret"), kind: providercontract.ErrorAuth},
		{name: "newline", raw: []byte("unit-test\nsecret"), kind: providercontract.ErrorAuth},
		{name: "oversize", raw: bytes.Repeat([]byte{'x'}, maximumKeyBytes+1), kind: providercontract.ErrorAuth},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hits := 0
			provider := test.provider
			if provider == nil && test.raw != nil {
				provider = secretProviderFunc(func(context.Context) ([]byte, error) { return test.raw, nil })
			}
			client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				hits++
			}), providercontract.NewManualClock(time.Now()), nil, provider)
			var dst fixtureEnvelope
			_, err := client.DoJSON(
				context.Background(), OperationOpenInterestHistory,
				openInterestHistoryPath, openInterestQuery(), &dst,
			)
			require.ErrorIs(t, err, &providercontract.ProviderError{Kind: test.kind, Provider: ProviderID})
			require.NotContains(t, err.Error(), "unit-test-secret-sentinel")
			require.Zero(t, hits)
			if test.raw != nil {
				require.Equal(t, make([]byte, len(test.raw)), test.raw, "disposable provider copy was not cleared")
			}
		})
	}

	t.Run("valid disposable copy", func(t *testing.T) {
		const sentinel = "disposable-secret-sentinel"
		raw := []byte(sentinel)
		client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, sentinel, r.Header.Get("CG-API-KEY"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(openInterestFixture)
		}), providercontract.NewManualClock(time.Now()), nil, secretProviderFunc(func(context.Context) ([]byte, error) {
			return raw, nil
		}))
		var dst fixtureEnvelope
		_, err := client.DoJSON(
			context.Background(), OperationOpenInterestHistory,
			openInterestHistoryPath, openInterestQuery(), &dst,
		)
		require.NoError(t, err)
		require.Equal(t, make([]byte, len(raw)), raw)
	})
}

func TestDoJSONMapsHTTPAndEnvelopeCodesToTypedErrors(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		status       int
		bodyCode     string
		kind         providercontract.ErrorKind
		retryAfter   string
		wantRetry    time.Duration
		wantFallback bool
	}{
		{"http 400", 400, "", providercontract.ErrorBadRequest, "", 0, false},
		{"http 401", 401, "", providercontract.ErrorAuth, "", 0, false},
		{"http 403", 403, "", providercontract.ErrorPermission, "", 0, false},
		{"http 408", 408, "", providercontract.ErrorTimeout, "", 0, true},
		{"http 429", 429, "", providercontract.ErrorRateLimit, "12", 12 * time.Second, true},
		{"http 500", 500, "", providercontract.ErrorUpstream5xx, "", 0, true},
		{"redirect", 307, "", providercontract.ErrorBadPayload, "", 0, false},
		{"body 400", 200, "400", providercontract.ErrorBadRequest, "", 0, false},
		{"body 401", 200, "401", providercontract.ErrorAuth, "", 0, false},
		{"body 403", 200, "403", providercontract.ErrorPermission, "", 0, false},
		{"body 408", 200, "408", providercontract.ErrorTimeout, "", 0, true},
		{"body 429", 200, "429", providercontract.ErrorRateLimit, "17", 17 * time.Second, true},
		{"body 500", 200, "500", providercontract.ErrorUpstream5xx, "", 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &observationRecorder{}
			client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.Header().Set("API-KEY-MAX-LIMIT", "30")
				w.Header().Set("API-KEY-USE-LIMIT", "30")
				if test.status == http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"code":"` + test.bodyCode + `","msg":"unit-test-secret-sentinel","data":[]}`))
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("unit-test-secret-sentinel upstream body must not escape"))
			}), providercontract.NewManualClock(now), recorder, fixedSecretProvider("unit-test-secret-sentinel"))
			var dst fixtureEnvelope
			_, err := client.DoJSON(
				context.Background(), OperationOpenInterestHistory,
				openInterestHistoryPath, openInterestQuery(), &dst,
			)
			require.ErrorIs(t, err, &providercontract.ProviderError{Kind: test.kind, Provider: ProviderID})
			require.Equal(t, test.wantFallback, providercontract.FallbackEligible(err))
			require.NotContains(t, err.Error(), "unit-test-secret-sentinel")
			var typedError *providercontract.ProviderError
			require.ErrorAs(t, err, &typedError)
			require.Equal(t, test.wantRetry, typedError.RetryAfter)
			values := recorder.Values()
			require.Len(t, values, 1)
			require.Equal(t, test.kind, values[0].ErrorKind)
			require.Equal(t, test.wantRetry, values[0].RetryAfter)
			require.Equal(t, 30, values[0].QuotaMaximum)
			require.Equal(t, 30, values[0].QuotaUsed)
		})
	}
}

func TestRetryAfterSupportsDeltaDateAndPlanWindowFallback(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"delta", "17", 17 * time.Second},
		{"date", now.Add(45 * time.Second).Format(http.TimeFormat), 45 * time.Second},
		{"missing", "", time.Minute},
		{"invalid", "tomorrow", time.Minute},
		{"past", now.Add(-time.Hour).Format(http.TimeFormat), time.Second},
		{"bounded", "999999999999999999", maximumRetryWait},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, parseRetryAfter(test.header, now))
		})
	}
}

func TestDoJSONRejectsWrongMediaMalformedSchemaTrailingAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"wrong media", "text/html", `{"code":"0","msg":"success","data":[]}`},
		{"missing media", "", `{"code":"0","msg":"success","data":[]}`},
		{"malformed", "application/json", `{"code":`},
		{"numeric code", "application/json", `{"code":0,"msg":"success","data":[]}`},
		{"missing code", "application/json", `{"msg":"success","data":[]}`},
		{"unknown envelope field", "application/json", `{"code":"0","msg":"success","data":[],"secret":"x"}`},
		{"unknown record field", "application/json", `{"code":"0","msg":"success","data":[{"time":1,"unexpected":"x"}]}`},
		{"trailing JSON", "application/json", `{"code":"0","msg":"success","data":[]} {}`},
		{"unknown code", "application/json", `{"code":"999","msg":"opaque","data":[]}`},
		{"oversized", "application/json", `{"code":"0","msg":"` + strings.Repeat("x", responseBodyLimit) + `","data":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.contentType != "" {
					w.Header().Set("Content-Type", test.contentType)
				}
				_, _ = w.Write([]byte(test.body))
			}), providercontract.NewManualClock(time.Now()), nil, fixedSecretProvider("test-only"))
			dst := fixtureEnvelope{Code: "sentinel"}
			_, err := client.DoJSON(
				context.Background(), OperationOpenInterestHistory,
				openInterestHistoryPath, openInterestQuery(), &dst,
			)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
			require.Equal(t, fixtureEnvelope{Code: "sentinel"}, dst)
		})
	}
}

func TestDoJSONNeverFollowsRedirects(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits++
	}))
	defer target.Close()
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}), providercontract.NewManualClock(time.Now()), nil, fixedSecretProvider("test-only"))
	var dst fixtureEnvelope
	_, err := client.DoJSON(
		context.Background(), OperationOpenInterestHistory,
		openInterestHistoryPath, openInterestQuery(), &dst,
	)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
	})
	require.Zero(t, targetHits)
}

func TestDoJSONClassifiesCancellationTimeoutNetworkAndTLSIdentity(t *testing.T) {
	t.Run("caller cancellation", func(t *testing.T) {
		client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("canceled request reached network")
		}), providercontract.NewManualClock(time.Now()), nil, fixedSecretProvider("test-only"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var dst fixtureEnvelope
		_, err := client.DoJSON(ctx, OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("timeout", func(t *testing.T) {
		client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}), providercontract.NewManualClock(time.Now()), nil, fixedSecretProvider("test-only"))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		var dst fixtureEnvelope
		_, err := client.DoJSON(ctx, OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorTimeout, Provider: ProviderID,
		})
	})

	t.Run("network", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		origin := "https://" + listener.Addr().String()
		require.NoError(t, listener.Close())
		transport := http.DefaultTransport.(*http.Transport).Clone()
		client, err := newTestClient(origin, transport, clientOptions{SecretProvider: fixedSecretProvider("test-only")})
		require.NoError(t, err)
		var dst fixtureEnvelope
		_, err = client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorNetwork, Provider: ProviderID,
		})
	})

	t.Run("TLS identity", func(t *testing.T) {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
		server.StartTLS()
		defer server.Close()
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}
		client, err := newTestClient(server.URL, transport, clientOptions{SecretProvider: fixedSecretProvider("test-only")})
		require.NoError(t, err)
		var dst fixtureEnvelope
		_, err = client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorInvalidIdentity, Provider: ProviderID,
		})
	})
}

func TestLocalBudgetCapsAtLowestPublishedPlanWithoutAnExtraRequest(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	clock := providercontract.NewManualClock(now)
	recorder := &observationRecorder{}
	hits := 0
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openInterestFixture)
	}), clock, recorder, fixedSecretProvider("test-only"))
	for range localRateLimit {
		var dst fixtureEnvelope
		_, err := client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
		require.NoError(t, err)
	}
	var dst fixtureEnvelope
	_, err := client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorRateLimit, Provider: ProviderID,
	})
	var typedError *providercontract.ProviderError
	require.ErrorAs(t, err, &typedError)
	require.Equal(t, time.Minute, typedError.RetryAfter)
	require.Equal(t, localRateLimit, hits)
	observations := recorder.Values()
	require.Len(t, observations, localRateLimit+1)
	blocked := observations[len(observations)-1]
	require.Equal(t, providercontract.ErrorRateLimit, blocked.ErrorKind)
	require.Equal(t, time.Minute, blocked.RetryAfter)
	require.Zero(t, blocked.StatusCode)

	clock.Advance(time.Minute)
	_, err = client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
	require.NoError(t, err)
	require.Equal(t, localRateLimit+1, hits)
}

func TestSecretNeverAppearsInURLSerializationObservationOrErrorLogs(t *testing.T) {
	const sentinel = "unit-test-secret-sentinel"
	var requestURL string
	recorder := &observationRecorder{}
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
		w.Header().Set("API-KEY-MAX-LIMIT", "30")
		w.Header().Set("API-KEY-USE-LIMIT", "1")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(sentinel))
	}), providercontract.NewManualClock(time.Now()), recorder, fixedSecretProvider(sentinel))
	var dst fixtureEnvelope
	_, err := client.DoJSON(context.Background(), OperationOpenInterestHistory, openInterestHistoryPath, openInterestQuery(), &dst)
	require.Error(t, err)

	serializedClient, marshalErr := json.Marshal(client)
	require.NoError(t, marshalErr)
	serializedObservation, marshalErr := json.Marshal(recorder.Values())
	require.NoError(t, marshalErr)
	var logged bytes.Buffer
	logger := log.New(&logged, "", 0)
	logger.Printf("%v", err)
	for label, value := range map[string]string{
		"request URL":            requestURL,
		"serialized client":      string(serializedClient),
		"serialized observation": string(serializedObservation),
		"error":                  err.Error(),
		"log":                    logged.String(),
	} {
		require.NotContains(t, value, sentinel, label)
	}
	require.NotContains(t, requestURL, "CG-API-KEY")
	require.NotContains(t, requestURL, "api_key")
}
