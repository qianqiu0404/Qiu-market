package coinglass

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

// Fixtures are checked-in examples shaped only from the current official
// CoinGlass V4 endpoint schemas. They contain no request headers or secrets.

//go:embed testdata/open_interest.json
var openInterestFixture []byte

//go:embed testdata/liquidation.json
var liquidationFixture []byte

type secretProviderFunc func(context.Context) ([]byte, error)

func (f secretProviderFunc) APIKey(ctx context.Context) ([]byte, error) {
	return f(ctx)
}

type observationRecorder struct {
	mu     sync.Mutex
	values []Observation
}

func (r *observationRecorder) Observe(value Observation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, value)
}

func (r *observationRecorder) Values() []Observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Observation(nil), r.values...)
}

func fixedSecretProvider(secret string) SecretProvider {
	return secretProviderFunc(func(context.Context) ([]byte, error) {
		return append([]byte(nil), secret...), nil
	})
}

func newTLSFixtureClient(
	t *testing.T,
	handler http.Handler,
	clock providercontract.Clock,
	sink ObservationSink,
	secretProvider SecretProvider,
) (*httpClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}
	client, err := newTestClient(server.URL, transport, clientOptions{
		Clock: clock, Sink: sink, SecretProvider: secretProvider,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		client.httpClient.CloseIdleConnections()
		server.Close()
	})
	return client, server
}

func openInterestQuery() url.Values {
	return url.Values{
		"exchange": {"Binance"}, "symbol": {"BTCUSD_PERP"},
		"interval": {"4h"}, "limit": {"2"}, "unit": {"usd"},
	}
}

func liquidationQuery() url.Values {
	return url.Values{
		"exchange": {"Binance"}, "symbol": {"BTCUSD_PERP"},
		"interval": {"4h"}, "limit": {"2"},
	}
}

type fixtureEnvelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data []fixtureRecord `json:"data"`
}

type fixtureRecord struct {
	Time                int64  `json:"time"`
	Open                string `json:"open,omitempty"`
	High                string `json:"high,omitempty"`
	Low                 string `json:"low,omitempty"`
	Close               string `json:"close,omitempty"`
	LongLiquidationUSD  string `json:"long_liquidation_usd,omitempty"`
	ShortLiquidationUSD string `json:"short_liquidation_usd,omitempty"`
}

func TestOfficialFixturesUseExactCredentialedTLSRequests(t *testing.T) {
	const secret = "unit-test-secret-sentinel"
	now := time.Date(2026, 8, 9, 19, 0, 0, 0, time.UTC)
	clock := providercontract.NewManualClock(now)
	recorder := &observationRecorder{}
	requests := 0
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		require.Equal(t, "qiu-market/providercontract-coinglass/1.0", r.Header.Get("User-Agent"))
		require.Equal(t, secret, r.Header.Get("CG-API-KEY"))
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.NotContains(t, r.URL.String(), secret)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("API-KEY-MAX-LIMIT", "30")
		w.Header().Set("API-KEY-USE-LIMIT", "2")
		switch r.URL.Path {
		case openInterestHistoryPath:
			require.Equal(t, openInterestQuery().Encode(), r.URL.Query().Encode())
			_, _ = w.Write(openInterestFixture)
		case liquidationHistoryPath:
			require.Equal(t, liquidationQuery().Encode(), r.URL.Query().Encode())
			_, _ = w.Write(liquidationFixture)
		default:
			http.NotFound(w, r)
		}
	}), clock, recorder, fixedSecretProvider(secret))

	var openInterest openInterestHistoryPayload
	receivedAt, err := client.DoJSON(
		context.Background(), OperationOpenInterestHistory,
		openInterestHistoryPath, openInterestQuery(), &openInterest,
	)
	require.NoError(t, err)
	require.Equal(t, now, receivedAt)
	require.Equal(t, "0", openInterest.Code)
	require.Len(t, openInterest.Data, 2)
	require.Equal(t, "6925000000.625", openInterest.Data[1].Close.String())
	openInterestResponse, err := mapOpenInterest(openInterest, receivedAt)
	require.NoError(t, err)
	openInterestEnvelope, ok := openInterestResponse.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.True(t, ok)
	require.Equal(t, "6925000000.625", openInterestEnvelope.Data.OpenInterest.Value)

	var liquidation liquidationHistoryPayload
	receivedAt, err = client.DoJSON(
		context.Background(), OperationLiquidationHistory,
		liquidationHistoryPath, liquidationQuery(), &liquidation,
	)
	require.NoError(t, err)
	require.Equal(t, now, receivedAt)
	require.Equal(t, "0", liquidation.Code)
	require.Len(t, liquidation.Data, 2)
	require.Equal(t, "8517330.44192", liquidation.Data[1].Short.String())
	liquidationResponse, err := mapLiquidation(liquidation, receivedAt)
	require.NoError(t, err)
	liquidationEnvelope, ok := liquidationResponse.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.True(t, ok)
	require.Equal(t, "5118407.85124", liquidationEnvelope.Data.LongLiquidations.Value)
	require.Equal(t, "8517330.44192", liquidationEnvelope.Data.ShortLiquidations.Value)
	require.Equal(t, 2, requests)

	observations := recorder.Values()
	require.Len(t, observations, 2)
	for _, observation := range observations {
		require.Equal(t, ProviderID, observation.Provider)
		require.Equal(t, providercontract.CapabilityDerivatives, observation.Capability)
		require.Equal(t, "success", observation.Outcome)
		require.Equal(t, http.StatusOK, observation.StatusCode)
		require.Equal(t, 30, observation.QuotaMaximum)
		require.Equal(t, 2, observation.QuotaUsed)
	}
}
