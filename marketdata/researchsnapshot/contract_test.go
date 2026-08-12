package researchsnapshot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCaptureBuildsDeterministicTwentyOneAssetSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v3/ticker/24hr" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		_ = json.NewEncoder(response).Encode([]binanceTicker{
			{Symbol: "BTCUSDT", OpenPrice: "1", HighPrice: "1", LowPrice: "1", LastPrice: "120000.10000000", Volume: "1", QuoteVolume: "1", CloseTime: now.Add(-time.Second).UnixMilli()},
			{Symbol: "ETHUSDT", OpenPrice: "1", HighPrice: "1", LowPrice: "1", LastPrice: "4500.20000000", Volume: "1", QuoteVolume: "1", CloseTime: now.Add(-2 * time.Second).UnixMilli()},
			{Symbol: "SOLUSDT", OpenPrice: "1", HighPrice: "1", LowPrice: "1", LastPrice: "230.30000000", Volume: "1", QuoteVolume: "1", CloseTime: now.Add(-3 * time.Second).UnixMilli()},
		})
	}))
	defer server.Close()

	client := routedTestClient(server)
	first, err := Capture(context.Background(), client, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Capture(context.Background(), client, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID || first.Checksum != second.Checksum {
		t.Fatal("same payload must produce the same identity")
	}
	if len(first.Coverage) != 21 || len(first.Quotes) != 3 {
		t.Fatalf("got %d coverage and %d quotes", len(first.Coverage), len(first.Quotes))
	}
	if first.Quotes[0].Price != "120000.1" || first.Quotes[0].Currency != "USDT" {
		t.Fatalf("unexpected quote %#v", first.Quotes[0])
	}
	if !strings.HasPrefix(first.SnapshotID, "market-2026-08-12-") {
		t.Fatalf("unexpected snapshot id %s", first.SnapshotID)
	}
}

func TestCaptureFailsClosedAsCompleteUnavailableCoverage(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	snapshot, err := Capture(context.Background(), routedTestClient(server), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Quotes) != 0 || len(snapshot.Coverage) != 21 {
		t.Fatalf("failed provider must not produce a partial contract: %#v", snapshot)
	}
	for _, coverage := range snapshot.Coverage[:3] {
		if coverage.Status != "unavailable" || coverage.Reason != "provider_http_429" {
			t.Fatalf("unexpected degraded coverage %#v", coverage)
		}
	}
}

func TestValidateRejectsMutationAndDuplicateCoverage(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	snapshot, err := Capture(context.Background(), &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: ioNopCloser{strings.NewReader("")}, Header: make(http.Header)}, nil
	})}, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Coverage[0].Status = "healthy"
	if err := Validate(snapshot); err == nil || (!strings.Contains(err.Error(), "requires a quote") && !strings.Contains(err.Error(), "checksum")) {
		t.Fatalf("expected contract mutation rejection, got %v", err)
	}
	snapshot, _ = Capture(context.Background(), &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: ioNopCloser{strings.NewReader("")}, Header: make(http.Header)}, nil
	})}, now)
	snapshot.Coverage[1].AssetID = snapshot.Coverage[0].AssetID
	if finalized, err := Finalize(snapshot); err == nil {
		t.Fatalf("expected duplicate rejection, got %#v", finalized)
	}
}

func TestValidateRejectsQuoteCoverageAndLicenseConflicts(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	base, err := Capture(context.Background(), &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: ioNopCloser{strings.NewReader("")}, Header: make(http.Header)}, nil
	})}, now)
	if err != nil {
		t.Fatal(err)
	}

	unavailableWithQuote := base
	unavailableWithQuote.Quotes = []Quote{{
		AssetID: "BTC-USDT", Role: "display", Price: "1", Currency: "USDT", ObservedAt: base.AsOf,
		Provider: "test", Mode: "live", DisplayScope: "private",
	}}
	if _, err := Finalize(unavailableWithQuote); err == nil || !strings.Contains(err.Error(), "cannot contain a quote") {
		t.Fatalf("expected unavailable quote rejection, got %v", err)
	}

	wrongScope := base
	wrongScope.Coverage[0] = Coverage{AssetID: "BTC-USDT", Status: "healthy", MarketState: "open"}
	wrongScope.Quotes = []Quote{{
		AssetID: "BTC-USDT", Role: "analysis", Price: "1", Currency: "USDT", ObservedAt: base.AsOf,
		Provider: "test", Mode: "live", DisplayScope: "private",
	}}
	if _, err := Finalize(wrongScope); err == nil || !strings.Contains(err.Error(), "role and display scope") {
		t.Fatalf("expected license-pair rejection, got %v", err)
	}

	falseFreshness := wrongScope
	falseFreshness.Quotes[0].Role = "display"
	falseFreshness.Quotes[0].DisplayScope = "private"
	falseFreshness.Quotes[0].DelaySeconds = 900
	if _, err := Finalize(falseFreshness); err == nil || (!strings.Contains(err.Error(), "delay conflicts") && !strings.Contains(err.Error(), "freshness conflicts")) {
		t.Fatalf("expected false freshness rejection, got %v", err)
	}
}

func routedTestClient(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.Host = target.Host
		return transport.RoundTrip(clone)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type ioNopCloser struct{ *strings.Reader }

func (ioNopCloser) Close() error { return nil }
