package fullstackgolden

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FixtureEvidence struct {
	SchemaVersion    string `json:"schema_version"`
	ResearchScenario string `json:"research_scenario"`
	ProviderScenario string `json:"provider_scenario"`
	ResearchReads    uint64 `json:"research_reads"`
	ProviderReads    uint64 `json:"provider_reads"`
	ControlWrites    uint64 `json:"control_writes"`
	NonGETRequests   uint64 `json:"non_get_requests"`
}

type fixtureServer struct {
	mu               sync.RWMutex
	researchScenario string
	providerScenario string
	logicalNow       time.Time
	researchReads    atomic.Uint64
	providerReads    atomic.Uint64
	controlWrites    atomic.Uint64
	nonGET           atomic.Uint64
}

// RunFixture serves deterministic upstream responses in a process separate
// from the coordinator. Every data endpoint is GET-only and loopback-only.
func RunFixture(ctx context.Context, address, certificatePath, keyPath string) error {
	if !filepath.IsAbs(certificatePath) || !filepath.IsAbs(keyPath) {
		return fmt.Errorf("full-stack fixture TLS paths must be absolute")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen full-stack fixture: %w", err)
	}
	ip := listener.Addr().(*net.TCPAddr).IP
	if !ip.IsLoopback() {
		_ = listener.Close()
		return fmt.Errorf("full-stack fixture must bind to loopback")
	}
	fixture := &fixtureServer{researchScenario: "fresh", providerScenario: "healthy", logicalNow: time.Now().UTC()}
	server := &http.Server{Handler: loopbackOnly(fixture), ReadHeaderTimeout: 2 * time.Second}
	done := make(chan error, 1)
	go func() {
		err := server.ServeTLS(listener, certificatePath, keyPath)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(server.Shutdown(shutdownContext), <-done)
	case err := <-done:
		return err
	}
}

func (f *fixtureServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/__fixture/control":
		f.control(writer, request)
		return
	case "/__fixture/evidence":
		if request.Method != http.MethodGet {
			f.nonGET.Add(1)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(writer, http.StatusOK, f.evidence())
		return
	}
	if request.Method != http.MethodGet {
		f.nonGET.Add(1)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if len(request.URL.Path) >= len("/api/market-radar/") && request.URL.Path[:len("/api/market-radar/")] == "/api/market-radar/" {
		f.research(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/provider/") || strings.HasPrefix(request.URL.Path, "/api/v3/") || strings.HasPrefix(request.URL.Path, "/api/futures/") {
		f.provider(writer, request)
		return
	}
	http.NotFound(writer, request)
}

func (f *fixtureServer) control(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Domain     string `json:"domain"`
		Scenario   string `json:"scenario"`
		ObservedAt string `json:"observed_at,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_control"})
		return
	}
	f.mu.Lock()
	if body.ObservedAt != "" {
		observed, parseErr := time.Parse(time.RFC3339Nano, body.ObservedAt)
		if parseErr != nil || observed.Location() != time.UTC {
			f.mu.Unlock()
			writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_observed_at"})
			return
		}
		f.logicalNow = observed
	}
	switch body.Domain {
	case "research":
		if !contains([]string{"fresh", "legacy", "empty", "degraded", "stale"}, body.Scenario) {
			f.mu.Unlock()
			writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_scenario"})
			return
		}
		f.researchScenario = body.Scenario
	case "provider":
		if !contains([]string{"healthy", "binance_429", "coinglass_5xx", "timeout", "bad_payload", "stale", "future", "conflict", "no_data", "cache_hit"}, body.Scenario) {
			f.mu.Unlock()
			writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_scenario"})
			return
		}
		f.providerScenario = body.Scenario
	default:
		f.mu.Unlock()
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_domain"})
		return
	}
	f.mu.Unlock()
	f.controlWrites.Add(1)
	writeJSON(writer, http.StatusOK, map[string]any{"domain": body.Domain, "scenario": body.Scenario, "wait_milliseconds": 150})
}

func (f *fixtureServer) research(writer http.ResponseWriter, request *http.Request) {
	f.researchReads.Add(1)
	scenario, _, now := f.scenarios()
	now = now.Truncate(time.Second)
	if scenario == "degraded" {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"code": "upstream_unavailable"})
		return
	}
	event := fixtureResearchEvent(now)
	if scenario == "legacy" {
		event["watchFor"], event["invalidation"] = nil, nil
	}
	if scenario == "stale" {
		event["occurredAt"] = now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
		event["publishedAt"] = now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	}
	writer.Header().Set("ETag", `"full-stack-`+scenario+`"`)
	writer.Header().Set("Last-Modified", now.Add(-time.Minute).Format(http.TimeFormat))
	switch request.URL.Path {
	case "/api/market-radar/summary":
		count := 1
		if scenario == "empty" {
			count = 0
		}
		sources := make([]map[string]any, 0, 5)
		for _, source := range []string{
			"github_releases",
			"sec_edgar",
			"federal_reserve",
			"binance_market_data",
			"qiu_market",
		} {
			sources = append(sources, map[string]any{
				"source": source, "health": "healthy",
				"lastSuccessAt": now.Add(-time.Minute).Format(time.RFC3339),
			})
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"status": "healthy", "generatedAt": now.Format(time.RFC3339), "latestEventAt": now.Add(-time.Hour).Format(time.RFC3339),
			"freshnessMinutes": 1, "isDelayed": scenario == "stale", "eventCount24h": count, "p0Count24h": 0, "p1Count24h": count,
			"sources": sources,
		})
	case "/api/market-radar/events":
		query := request.URL.Query()
		if query.Get("market") != "crypto" || query.Get("asset") != "BTC" || query.Get("window") != "168" {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_query"})
			return
		}
		items := []any{event}
		if scenario == "empty" {
			items = []any{}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"status": "healthy", "items": items, "nextCursor": nil})
	case "/api/market-radar/events/btc-full-stack-event":
		writeJSON(writer, http.StatusOK, event)
	default:
		http.NotFound(writer, request)
	}
}

func (f *fixtureServer) provider(writer http.ResponseWriter, request *http.Request) {
	f.providerReads.Add(1)
	_, scenario, now := f.scenarios()
	if scenario == "timeout" {
		time.Sleep(400 * time.Millisecond)
	}
	if scenario == "binance_429" && (request.URL.Path == "/api/v3/ticker/24hr" || request.URL.Path == "/api/v3/klines") {
		writer.Header().Set("Retry-After", "1")
		writeJSON(writer, http.StatusTooManyRequests, map[string]string{"code": "rate_limit"})
		return
	}
	if scenario == "coinglass_5xx" && strings.HasPrefix(request.URL.Path, "/api/futures/") {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"code": "upstream_5xx"})
		return
	}
	if scenario == "bad_payload" {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte("{"))
		return
	}
	now = now.UTC().Truncate(time.Millisecond)
	eventTime := now
	if scenario == "stale" {
		eventTime = now.Add(-6 * time.Hour)
	}
	if scenario == "future" {
		eventTime = now.Add(time.Hour)
	}
	switch request.URL.Path {
	case "/api/v3/ticker/24hr":
		writeJSON(writer, http.StatusOK, map[string]any{
			"symbol": "BTCUSDT", "priceChange": "100.00000000", "priceChangePercent": "0.16666667",
			"weightedAvgPrice": "59950.00000000", "prevClosePrice": "59900.00000000", "lastPrice": "60000.00000000",
			"lastQty": "0.01000000", "bidPrice": "59999.00000000", "bidQty": "1.00000000", "askPrice": "60001.00000000",
			"askQty": "1.00000000", "openPrice": "59900.00000000", "highPrice": "61000.00000000", "lowPrice": "59000.00000000",
			"volume": "100.00000000", "quoteVolume": "6000000.00000000", "openTime": eventTime.Add(-24 * time.Hour).UnixMilli(),
			"closeTime": eventTime.UnixMilli(), "firstId": 1, "lastId": 2, "count": 2,
		})
	case "/api/v3/klines":
		rows := make([][]any, 0, 2)
		for offset := 2; offset >= 1; offset-- {
			open := eventTime.Add(-time.Duration(offset) * time.Minute).Truncate(time.Minute)
			closeTime := open.Add(time.Minute).Add(-time.Millisecond).UnixMilli()
			if scenario == "conflict" && offset == 1 {
				closeTime--
			}
			rows = append(rows, []any{open.UnixMilli(), "60000.00000000", "60010.00000000", "59990.00000000", "60005.00000000", "1.00000000", closeTime, "60005.00000000", 10, "0.50000000", "30002.50000000", "0"})
		}
		writeJSON(writer, http.StatusOK, rows)
	case "/api/futures/open-interest/history":
		writeJSON(writer, http.StatusOK, map[string]any{"code": "0", "msg": "success", "data": []map[string]any{
			{"time": eventTime.Add(-4 * time.Hour).UnixMilli(), "open": "6800000000.000", "high": "6900000000.000", "low": "6700000000.000", "close": "6850000000.000"},
			{"time": eventTime.UnixMilli(), "open": "6850000000.000", "high": "7000000000.000", "low": "6800000000.000", "close": "6925000000.625"},
		}})
	case "/api/futures/liquidation/history":
		writeJSON(writer, http.StatusOK, map[string]any{"code": "0", "msg": "success", "data": []map[string]any{
			{"time": eventTime.Add(-4 * time.Hour).UnixMilli(), "long_liquidation_usd": "1000000.00000", "short_liquidation_usd": "2000000.00000"},
			{"time": eventTime.UnixMilli(), "long_liquidation_usd": "5118407.85124", "short_liquidation_usd": "8517330.44192"},
		}})
	default:
		http.NotFound(writer, request)
	}
}

func (f *fixtureServer) scenarios() (string, string, time.Time) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.researchScenario, f.providerScenario, f.logicalNow
}

func (f *fixtureServer) evidence() FixtureEvidence {
	research, provider, _ := f.scenarios()
	return FixtureEvidence{SchemaVersion: "qiu.full-stack.fixture-evidence.v1", ResearchScenario: research, ProviderScenario: provider,
		ResearchReads: f.researchReads.Load(), ProviderReads: f.providerReads.Load(), ControlWrites: f.controlWrites.Load(), NonGETRequests: f.nonGET.Load()}
}

func fixtureResearchEvent(now time.Time) map[string]any {
	return map[string]any{
		"id": "btc-full-stack-event", "slug": "btc-full-stack-event", "market": "crypto", "priority": "P1", "score": 80,
		"titleZh": "BTC 全栈确定性研究信号", "summaryZh": "受限只读fixture，不构成交易指令。", "whyItMattersZh": "验证研究信号链路。",
		"watchFor": "等待官方来源确认。", "invalidation": "官方来源撤回。", "eventType": "release", "newsDirection": "neutral",
		"systemJudgment": "仅供研究展示。", "horizon": "days", "occurredAt": now.Add(-time.Hour).Format(time.RFC3339),
		"publishedAt": now.Add(-30 * time.Minute).Format(time.RFC3339), "sourceCount": 1,
		"sources": []map[string]any{{"name": "Bitcoin Core", "url": "https://github.com/bitcoin/bitcoin/releases"}},
		"assets":  []map[string]any{{"namespace": "crypto", "symbol": "BTC", "relevance": 100}}, "reaction": nil,
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
