package binancepublic

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

// Fixtures are checked-in examples shaped only from Binance's official public
// market endpoint schemas; tests never contact the real service.

//go:embed testdata/ticker.json
var tickerFixture []byte

//go:embed testdata/klines.json
var klinesFixture []byte

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

func newTLSFixtureClient(
	t *testing.T,
	handler http.Handler,
	clock providercontract.Clock,
	sink ObservationSink,
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
	client, err := newTestClient(server.URL, transport, clientOptions{Clock: clock, Sink: sink})
	require.NoError(t, err)
	t.Cleanup(func() {
		client.httpClient.CloseIdleConnections()
		server.Close()
	})
	return client, server
}

func tickerQuery() url.Values {
	return url.Values{
		"symbol": {"BTCUSDT"}, "type": {"FULL"}, "symbolStatus": {"TRADING"},
	}
}

func klinesQuery() url.Values {
	return url.Values{
		"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"10"}, "timeZone": {"0"},
	}
}

func TestOfficialFixturesUseExactTLSRequestsAndLowCardinalityMetrics(t *testing.T) {
	now := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	clock := providercontract.NewManualClock(now)
	recorder := &observationRecorder{}
	requests := 0
	client, _ := newTLSFixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		require.Equal(t, "qiu-market/providercontract-binancepublic/1.0", r.Header.Get("User-Agent"))
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case tickerPath:
			require.Equal(t, tickerQuery().Encode(), r.URL.Query().Encode())
			_, _ = w.Write(tickerFixture)
		case klinesPath:
			require.Equal(t, klinesQuery().Encode(), r.URL.Query().Encode())
			_, _ = w.Write(klinesFixture)
		default:
			http.NotFound(w, r)
		}
	}), clock, recorder)

	var ticker struct {
		Symbol    string `json:"symbol"`
		LastPrice string `json:"lastPrice"`
		CloseTime int64  `json:"closeTime"`
	}
	receivedAt, err := client.DoJSON(
		context.Background(), OperationTicker24h, tickerPath, tickerQuery(), &ticker,
	)
	require.NoError(t, err)
	require.Equal(t, now, receivedAt)
	require.Equal(t, "BTCUSDT", ticker.Symbol)
	require.Equal(t, "60000.00000000", ticker.LastPrice)

	var klines [][]any
	receivedAt, err = client.DoJSON(
		context.Background(), OperationKlines, klinesPath, klinesQuery(), &klines,
	)
	require.NoError(t, err)
	require.Equal(t, now, receivedAt)
	require.Len(t, klines, 1)
	require.Equal(t, 2, requests)

	observations := recorder.Values()
	require.Len(t, observations, 2)
	require.Equal(t, Observation{
		Provider: ProviderID, Operation: OperationTicker24h,
		Capability: providercontract.CapabilitySpotTicker,
		Outcome:    "success", StatusCode: http.StatusOK, ResponseBytes: int64(len(tickerFixture)),
	}, observations[0])
	require.Equal(t, Observation{
		Provider: ProviderID, Operation: OperationKlines,
		Capability: providercontract.CapabilityOHLCV,
		Outcome:    "success", StatusCode: http.StatusOK, ResponseBytes: int64(len(klinesFixture)),
	}, observations[1])
}
