// Command quality-golden serves a deterministic read-only quality monitor and
// the real data-quality HTTP handler. It has no database or trading dependency.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/qualityadapters"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
	"github.com/the-web3/s78-market-services/services/http/dataquality"
	"github.com/the-web3/s78-market-services/services/http/researchsignals"
)

const apiAddress = "127.0.0.1:19097"

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type evidenceCounters struct {
	qualityReads     atomic.Int64
	legacyReads      atomic.Int64
	tradingMutations atomic.Int64
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	monitor, err := goldenMonitor(now)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", apiAddress)
	if err != nil {
		return fmt.Errorf("listen quality API: %w", err)
	}
	counters := &evidenceCounters{}
	router := chi.NewRouter()
	dataquality.Mount(router, countingReporter{Reporter: monitor, reads: &counters.qualityReads})
	researchsignals.Mount(router, nil)
	mountLegacyReadFixtures(router, &counters.legacyReads)
	router.Get("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"status": "ready", "schemaVersion": "quality-golden/v1", "tradingMutations": counters.tradingMutations.Load()})
	})
	router.Get("/__fixture/evidence", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{
			"schemaVersion": "quality-golden-evidence/v1", "qualityReads": counters.qualityReads.Load(),
			"legacyReadRequests": counters.legacyReads.Load(), "tradingMutations": counters.tradingMutations.Load(),
			"providerNetworkRequests": 0, "databaseWrites": 0,
		})
	})
	server := &http.Server{Handler: router, ReadHeaderTimeout: 2 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	log.Printf("quality golden ready api=http://%s health=http://%s/healthz", apiAddress, apiAddress)
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil {
			return err
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(shutdownContext)
}

type countingReporter struct {
	dataquality.Reporter
	reads *atomic.Int64
}

func (r countingReporter) Reports() (quality.ReportSet, error) {
	r.reads.Add(1)
	return r.Reporter.Reports()
}

func goldenMonitor(now time.Time) (*quality.Monitor, error) {
	monitor, err := quality.NewMonitor(quality.DefaultPolicies(), fixedClock{now: now})
	if err != nil {
		return nil, err
	}
	perfect := map[quality.Metric]quality.Counters{
		quality.MetricFreshness: {Numerator: 1, Denominator: 1}, quality.MetricAvailability: {Numerator: 1, Denominator: 1},
		quality.MetricCompleteness: {Numerator: 1, Denominator: 1}, quality.MetricSchema: {Numerator: 1, Denominator: 1},
		quality.MetricConsistency: {Numerator: 1, Denominator: 1}, quality.MetricCoverage: {Numerator: 1, Denominator: 1},
	}
	sequence := 0
	record := func(source quality.SourceKind, capability quality.Capability, license quality.LicenseStatus, live bool, offset time.Duration) error {
		sequence++
		return monitor.Record(quality.Evidence{
			ID: fmt.Sprintf("golden-%02d", sequence), Source: source, Capability: capability,
			At: now.Add(offset), Outcome: quality.OutcomeSuccess, Latency: 20 * time.Millisecond,
			License: license, Live: live, Metrics: perfect,
			Ref: quality.EvidenceRef{SourceID: fmt.Sprintf("quality-golden:%02d", sequence)},
		})
	}
	for index := 0; index < 5; index++ {
		if err := record(quality.SourceBinanceSpot, quality.CapabilitySpotTicker, quality.LicenseApproved, true, -time.Duration(5-index)*time.Second); err != nil {
			return nil, err
		}
	}
	for index := 0; index < 2; index++ {
		if err := record(quality.SourceBinanceSpot, quality.CapabilityOHLCV, quality.LicenseApproved, true, -time.Duration(2-index)*time.Second); err != nil {
			return nil, err
		}
	}
	for _, capability := range []quality.Capability{quality.CapabilityOpenInterest, quality.CapabilityLiquidation} {
		if err := record(quality.SourceCoinGlassDerivative, capability, quality.LicenseRestricted, false, -time.Hour); err != nil {
			return nil, err
		}
	}
	// Exercise the production Recorder seam: one summary fetch and one event
	// list fetch become exactly two immutable research evidence facts.
	if err := qualityadapters.RecordResearchSummary(monitor, "golden-research-summary", now.Add(-2*time.Second), 15*time.Millisecond, researchsignal.SummaryResult{
		Status: researchsignal.StatusFresh,
		Data:   researchsignal.Summary{Sources: []researchsignal.SourceStatus{{Source: "market_radar", Status: researchsignal.SourceHealthy}}},
	}, nil, true); err != nil {
		return nil, err
	}
	watch, invalidation := "watch official confirmation", "publisher retracts"
	if err := qualityadapters.RecordResearchEvents(monitor, "golden-research-events", now.Add(-time.Second), 18*time.Millisecond, researchsignal.ListResult{
		Status: researchsignal.StatusFresh,
		Data: researchsignal.EventList{Items: []researchsignal.Signal{{
			ID: "golden-event", Source: "xiuqiu-site Market Radar", Provider: researchsignal.Provider,
			SourceURL: "https://xiuqiu-site.vercel.app/market-radar/events/golden-event",
			EventTime: now.Add(-time.Hour).Format(time.RFC3339Nano), PublishedAt: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), Priority: "P1", Freshness: researchsignal.FreshnessFresh,
			WatchFor: &watch, Invalidation: &invalidation,
		}}}, Quality: researchsignal.QualityStats{InputCount: 1, OutputCount: 1},
	}, nil, true); err != nil {
		return nil, err
	}
	for index := 0; index < 3; index++ {
		if _, err := monitor.Advance(now.Add(time.Duration(index) * time.Nanosecond)); err != nil {
			return nil, err
		}
	}
	return monitor, nil
}

func mountLegacyReadFixtures(router chi.Router, reads *atomic.Int64) {
	respond := func(result any, total *int) http.HandlerFunc {
		return func(writer http.ResponseWriter, _ *http.Request) {
			reads.Add(1)
			body := map[string]any{"code": 2000, "message": "success", "result": result}
			if total != nil {
				body["total"] = *total
			}
			writeJSON(writer, body)
		}
	}
	zero := 0
	router.Post("/api/v1/get_market_insights", respond(map[string]any{}, nil))
	router.Post("/api/v2/get_asset_dashboard", respond([]any{}, &zero))
	router.Post("/api/v1/get_system_overview", respond(map[string]any{"dw_status": "unavailable"}, nil))
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(writer).Encode(value)
}
