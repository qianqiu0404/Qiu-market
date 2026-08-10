package researchsignal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestProductionClientHasNoEnvironmentProxy(t *testing.T) {
	t.Parallel()
	client, err := New(Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("production transport must not inherit an environment proxy")
	}
}

func TestClientStrictQueryAndValidatedCache(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/api/market-radar/events" || request.URL.Query().Get("market") != "crypto" || request.URL.Query().Get("asset") != "BTC" || request.URL.Query().Get("window") != "168" {
			t.Errorf("unexpected request: %s", request.URL.String())
		}
		event := fixtureEvent(now)
		_ = json.NewEncoder(writer).Encode(upstreamList{Status: "healthy", Items: []upstreamEvent{event}})
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	query := EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20}
	for index := 0; index < 2; index++ {
		result, err := client.Events(t.Context(), query)
		if err != nil || result.Status != StatusFresh || len(result.Data.Items) != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want validated cache hit", requests.Load())
	}
	bad := url.Values{"market": {"crypto"}, "asset": {"BTC"}, "window": {"168"}, "limit": {"20"}, "foo": {"bar"}}
	if err := client.validateTarget("/api/market-radar/events", bad); err == nil {
		t.Fatal("unexpected query key accepted")
	}
}

func TestNormalizeSummaryPreservesSourceHealthWire(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	latest := now.Add(-time.Hour).Format(time.RFC3339)
	lastSuccess := now.Add(-time.Minute).Format(time.RFC3339)
	input := upstreamSummary{
		Status: "healthy", GeneratedAt: now.Format(time.RFC3339), LatestEventAt: &latest,
		FreshnessMinutes: ptr(int64(1)), EventCount24h: 3, P0Count24h: 1, P1Count24h: 1,
		Sources: []upstreamSourceStatus{
			{Source: "github_releases", Health: "healthy", LastSuccessAt: &lastSuccess},
			{Source: "sec_edgar", Health: "healthy", LastSuccessAt: &lastSuccess},
			{Source: "federal_reserve", Health: "healthy", LastSuccessAt: &lastSuccess},
			{Source: "binance_market_data", Health: "healthy", LastSuccessAt: &lastSuccess},
			{Source: "qiu_market", Health: "degraded"},
		},
	}
	result, err := normalizeSummary(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFresh || result.Data.Sources[0].Status != SourceHealthy || result.Data.Sources[4].Status != SourceDegraded {
		t.Fatalf("result=%+v", result)
	}
	payload, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"status":"healthy"`)) {
		t.Fatalf("source health wire changed: %s", payload)
	}
}

func TestListStatusMatrix(t *testing.T) {
	t.Parallel()
	fresh := Signal{Freshness: FreshnessFresh}
	legacy := fresh
	legacy.QualityFlags = []string{"legacy_fields_missing"}
	stale := Signal{Freshness: FreshnessStale}
	tests := []struct {
		name    string
		items   []Signal
		partial bool
		want    Status
	}{
		{"fresh", []Signal{fresh}, false, StatusFresh},
		{"empty", nil, false, StatusEmpty},
		{"legacy", []Signal{legacy}, false, StatusLegacy},
		{"mixed legacy", []Signal{fresh, legacy}, false, StatusPartial},
		{"stale", []Signal{stale}, false, StatusStale},
		{"mixed stale", []Signal{fresh, stale}, false, StatusPartial},
		{"conflict suppresses all", nil, true, StatusPartial},
	}
	for _, test := range tests {
		if got := listStatus(StatusFresh, test.items, test.partial); got != test.want {
			t.Errorf("%s: got %s want %s", test.name, got, test.want)
		}
	}
}

func ptr[T any](value T) *T { return &value }

func TestBadPayloadIsNotCached(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = writer.Write([]byte(`{"status":"healthy","items":[],"unknown":true}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(upstreamList{Status: "healthy", Items: []upstreamEvent{fixtureEvent(now)}})
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	query := EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20}
	if _, err := client.Events(t.Context(), query); err == nil {
		t.Fatal("malformed schema accepted")
	}
	if result, err := client.Events(t.Context(), query); err != nil || len(result.Data.Items) != 1 {
		t.Fatalf("second request result=%+v err=%v", result, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, bad payload was cached", requests.Load())
	}
}

func TestConditionalRequestRefreshesValidatedCache(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	clock := now
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("If-None-Match") == `"fixture-v1"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("ETag", `"fixture-v1"`)
		_ = json.NewEncoder(writer).Encode(upstreamList{Status: "healthy", Items: []upstreamEvent{fixtureEvent(now)}})
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	query := EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20}
	if _, err := client.Events(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(cacheTTL + time.Second)
	if _, err := client.Events(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want conditional refresh", requests.Load())
	}
}

func TestRateLimitBlocksWithoutSleeping(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Retry-After", "999999999999")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	query := EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20}
	started := time.Now()
	_, first := client.Events(t.Context(), query)
	_, second := client.Events(t.Context(), query)
	if first == nil || second == nil || time.Since(started) > time.Second || requests.Load() != 1 {
		t.Fatalf("first=%v second=%v requests=%d elapsed=%s", first, second, requests.Load(), time.Since(started))
	}
	if typedError, ok := first.(*Error); !ok || typedError.RetryAfter != 15*time.Minute {
		t.Fatalf("retry after not bounded: %v", first)
	}
}

func TestDetailBindsRequestedID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		event := fixtureEvent(now)
		if request.URL.Path == "/api/market-radar/events/evt:one" {
			event.ID, event.Slug = "evt:one", "evt:one"
		} else {
			event.ID, event.Slug = "different", "different"
		}
		_ = json.NewEncoder(writer).Encode(event)
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if result, err := client.Event(t.Context(), "evt:one"); err != nil || result.Data.Item == nil || result.Data.Item.ID != "evt:one" {
		t.Fatalf("colon id result=%+v err=%v", result, err)
	}
	if _, err := client.Event(t.Context(), "requested"); err == nil {
		t.Fatal("mismatched body id accepted")
	}
}

func TestContentTypeAndBodyBoundsFailClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	tests := map[string]http.HandlerFunc{
		"wrong content type": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte(`{"status":"healthy","items":[]}`))
		},
		"oversized": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(make([]byte, maxResponseBytes+1))
		},
	}
	for name, handler := range tests {
		name, handler := name, handler
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(handler)
			defer server.Close()
			client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Events(t.Context(), EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20})
			if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestRetryIsBoundedToOneRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := newTestClient(server.URL, server.Client(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Events(t.Context(), EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20})
	if err == nil || requests.Load() != 2 {
		t.Fatalf("err=%v requests=%d", err, requests.Load())
	}
}
