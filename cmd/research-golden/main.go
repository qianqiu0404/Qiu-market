// Command research-golden runs a deterministic xiuqiu-site fixture and the
// real Qiu research HTTP handler. It has no database or trading dependency.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
	"github.com/the-web3/s78-market-services/services/http/dataquality"
	"github.com/the-web3/s78-market-services/services/http/researchsignals"
)

const (
	upstreamAddress = "127.0.0.1:19095"
	apiAddress      = "127.0.0.1:19096"
)

type fixture struct {
	reads          atomic.Int64
	upstreamNonGET atomic.Int64
	controlWrites  atomic.Int64
	scenarioMutex  sync.RWMutex
	scenario       string
	legacyReads    *atomic.Int64
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	upstreamListener, err := net.Listen("tcp", upstreamAddress)
	if err != nil {
		return fmt.Errorf("listen fixture: %w", err)
	}
	apiListener, err := net.Listen("tcp", apiAddress)
	if err != nil {
		_ = upstreamListener.Close()
		return fmt.Errorf("listen API: %w", err)
	}

	legacyReads := &atomic.Int64{}
	upstream := &fixture{scenario: "success", legacyReads: legacyReads}
	upstreamServer := &http.Server{Handler: upstream, ReadHeaderTimeout: 2 * time.Second}
	reader, err := researchsignal.NewGoldenFixtureReader()
	if err != nil {
		_ = upstreamListener.Close()
		_ = apiListener.Close()
		return err
	}
	router := chi.NewRouter()
	researchsignals.Mount(router, reader)
	// Insights now always renders the read-only quality panel. This older
	// research golden intentionally has no quality collector, so mount the real
	// handler in its honest unconfigured/insufficient state.
	dataquality.Mount(router, nil)
	router.Get("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"status": "ready", "schemaVersion": "research-golden/v1",
			"upstream": "http://127.0.0.1:19095", "tradingMutations": 0,
		})
	})
	mountLegacyReadFixtures(router, legacyReads)
	apiServer := &http.Server{Handler: router, ReadHeaderTimeout: 2 * time.Second}

	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- serve(upstreamServer, upstreamListener) }()
	go func() { errorsChannel <- serve(apiServer, apiListener) }()
	log.Printf("research golden ready upstream=http://%s api=http://%s health=http://%s/healthz", upstreamAddress, apiAddress, apiAddress)

	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil {
			return err
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return errors.Join(apiServer.Shutdown(shutdownContext), upstreamServer.Shutdown(shutdownContext))
}

func serve(server *http.Server, listener net.Listener) error {
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (f *fixture) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/__fixture/evidence" {
		if request.Method != http.MethodGet {
			f.upstreamNonGET.Add(1)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"schemaVersion": "research-golden-evidence/v1", "upstreamReads": f.reads.Load(),
			"upstreamNonGet": f.upstreamNonGET.Load(), "fixtureControlWrites": f.controlWrites.Load(),
			"scenario": f.currentScenario(), "tradingMutations": 0,
			"legacyReadRequests": f.legacyReads.Load(),
		})
		return
	}
	if request.URL.Path == "/__fixture/control" {
		f.control(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		f.upstreamNonGET.Add(1)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	f.reads.Add(1)
	scenario := f.currentScenario()
	if scenario == "error" {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"code": "fixture_error"})
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	etag := `"research-golden-` + scenario + `"`
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Last-Modified", "Sun, 10 Aug 2026 00:00:00 GMT")
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	event := fixtureEvent(now)
	if scenario == "legacy" {
		event["watchFor"] = nil
		event["invalidation"] = nil
	}
	switch request.URL.Path {
	case "/api/market-radar/summary":
		lastSuccess := now.Add(-time.Minute).Format(time.RFC3339)
		latest := now.Add(-time.Hour).Format(time.RFC3339)
		status := "healthy"
		isDelayed := false
		eventCount := 1
		if scenario == "degraded" {
			status, isDelayed = "degraded", true
		}
		if scenario == "empty" {
			eventCount = 0
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"status": status, "generatedAt": now.Format(time.RFC3339), "latestEventAt": latest,
			"freshnessMinutes": 1, "isDelayed": isDelayed, "eventCount24h": eventCount, "p0Count24h": 0, "p1Count24h": min(eventCount, 1),
			"sources": []map[string]any{
				{"source": "github_releases", "health": "healthy", "lastSuccessAt": lastSuccess},
				{"source": "sec_edgar", "health": "healthy", "lastSuccessAt": lastSuccess},
				{"source": "federal_reserve", "health": "healthy", "lastSuccessAt": lastSuccess},
				{"source": "binance_market_data", "health": "healthy", "lastSuccessAt": lastSuccess},
				{"source": "qiu_market", "health": "healthy", "lastSuccessAt": lastSuccess},
			},
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
	case "/api/market-radar/events/btc-golden-event":
		writeJSON(writer, http.StatusOK, event)
	default:
		writeJSON(writer, http.StatusNotFound, map[string]string{"code": "event_not_found"})
	}
}

func mountLegacyReadFixtures(router chi.Router, reads *atomic.Int64) {
	respond := func(result any, total *int) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			reads.Add(1)
			body := map[string]any{"code": 2000, "message": "success", "result": result}
			if total != nil {
				body["total"] = *total
			}
			writeJSON(writer, http.StatusOK, body)
		}
	}
	zero := 0
	// Deterministic read-only responses required by the existing Insights shell.
	// They neither proxy an upstream nor touch storage or trading state.
	router.Post("/api/v1/get_market_insights", respond(map[string]any{}, nil))
	router.Post("/api/v2/get_asset_dashboard", respond([]any{}, &zero))
	router.Post("/api/v1/get_system_overview", respond(map[string]any{"dw_status": "unavailable"}, nil))
}

func (f *fixture) currentScenario() string {
	f.scenarioMutex.RLock()
	defer f.scenarioMutex.RUnlock()
	return f.scenario
}

func (f *fixture) control(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 1024)
	var body struct {
		Scenario string `json:"scenario"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_control"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_control"})
		return
	}
	allowed := map[string]bool{"success": true, "legacy": true, "empty": true, "degraded": true, "error": true}
	if !allowed[body.Scenario] {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_scenario"})
		return
	}
	f.scenarioMutex.Lock()
	f.scenario = body.Scenario
	f.scenarioMutex.Unlock()
	f.controlWrites.Add(1)
	writeJSON(writer, http.StatusOK, map[string]any{"scenario": body.Scenario, "waitMilliseconds": 150})
}

func fixtureEvent(now time.Time) map[string]any {
	return map[string]any{
		"id": "btc-golden-event", "slug": "btc-golden-event", "market": "crypto", "priority": "P1", "score": 72,
		"titleZh": "BTC 确定性研究信号", "summaryZh": "只读研究事件，不构成交易指令。",
		"whyItMattersZh": "验证公开研究内容从上游到 Qiu API 的真实链路。",
		"watchFor":       "等待下一条官方来源确认。", "invalidation": "官方来源撤回或更正。",
		"eventType": "release", "newsDirection": "neutral", "systemJudgment": "仅供研究展示。", "horizon": "days",
		"occurredAt": now.Add(-time.Hour).Format(time.RFC3339), "publishedAt": now.Add(-30 * time.Minute).Format(time.RFC3339),
		"sourceCount": 1,
		"sources":     []map[string]any{{"name": "Bitcoin Core", "url": "https://github.com/bitcoin/bitcoin/releases"}},
		"assets":      []map[string]any{{"namespace": "crypto", "symbol": "BTC", "relevance": 100}},
		"reaction":    nil,
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
