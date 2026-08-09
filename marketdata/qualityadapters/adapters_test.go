package qualityadapters

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/binancepublic"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/coinglass"
	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

func TestBinanceSuccessCombinesObservationAndNormalizedDispatchOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	result := tickerResult(now, []providercontract.QualityFlag{providercontract.QualityDuplicate})
	evidence, err := BinanceEvidence("binance-ticker-1", now, binancepublic.Observation{
		Provider: binancepublic.ProviderID, Capability: providercontract.CapabilitySpotTicker,
		Outcome: "success", Duration: 25 * time.Millisecond,
	}, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.License != quality.LicenseUnknown || !evidence.Live || evidence.CacheHit || evidence.Outcome != quality.OutcomeSuccess {
		t.Fatalf("evidence=%+v", evidence)
	}
	if got := evidence.Metrics[quality.MetricConsistency]; got.Numerator != 0 || got.Denominator != 1 {
		t.Fatalf("duplicate consistency=%+v", got)
	}
	if evidence.Ref.SourceID != "binance-public:BTCUSDT:"+strconv.FormatInt(now.Add(-time.Second).UnixMilli(), 10) {
		t.Fatalf("unsafe or unstable ref=%+v", evidence.Ref)
	}
}

func TestCacheHitHasNoHTTPObservationAndCannotAdvanceAttemptOrRecovery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	result := tickerResult(now, nil)
	result.Trace.CacheHit = true
	result.Trace.Attempts[0].CacheHit = true
	evidence, err := BinanceCacheEvidence("binance-cache-1", now, providercontract.CapabilitySpotTicker, result)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.CacheHit || evidence.Latency != 0 || evidence.Metrics != nil {
		t.Fatalf("cache evidence=%+v", evidence)
	}
	window, err := quality.NewEvidenceWindow(now.Add(-5*time.Minute), now.Add(time.Nanosecond), 7, []quality.Evidence{evidence})
	if err != nil {
		t.Fatal(err)
	}
	report, err := quality.Evaluate(quality.DefaultPolicies()[quality.SourceBinanceSpot], window)
	if err != nil {
		t.Fatal(err)
	}
	if report.AttemptCount != 0 || report.SuccessCount != 0 || report.Status != quality.StatusInsufficient {
		t.Fatalf("cache advanced quality readiness: %+v", report)
	}

	observation := binancepublic.Observation{Provider: binancepublic.ProviderID, Capability: providercontract.CapabilitySpotTicker, Outcome: "success"}
	if _, err := BinanceEvidence("fake-http-cache", now, observation, result, nil); err == nil {
		t.Fatal("cache hit accepted a fabricated HTTP observation")
	}
}

func TestProviderErrorsRemainTypedAndDoNotInventSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	fetchErr := providercontract.NewRetryError(providercontract.ErrorRateLimit, binancepublic.ProviderID, "ticker", time.Minute, errors.New("limited"))
	evidence, err := BinanceEvidence("binance-rate-1", now, binancepublic.Observation{
		Provider: binancepublic.ProviderID, Capability: providercontract.CapabilitySpotTicker,
		Outcome: "error", ErrorKind: providercontract.ErrorRateLimit, Duration: time.Millisecond,
	}, providercontract.DispatchResult{}, fetchErr)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Outcome != quality.OutcomeRateLimit || evidence.Metrics != nil || evidence.Live != true {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestProviderSuccessFailsClosedOnZeroOrCrossProviderDispatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	observation := binancepublic.Observation{Provider: binancepublic.ProviderID, Capability: providercontract.CapabilitySpotTicker, Outcome: "success"}
	if _, err := BinanceEvidence("zero-result", now, observation, providercontract.DispatchResult{}, nil); err == nil {
		t.Fatal("zero dispatch result was accepted")
	}
	cross := tickerResult(now, nil)
	cross.Trace.ActualProvider = coinglass.ProviderID
	if _, err := BinanceEvidence("cross-result", now, observation, cross, nil); err == nil {
		t.Fatal("cross-provider dispatch result was accepted")
	}
	badOutcome := tickerResult(now, nil)
	observation.Outcome = "error"
	observation.ErrorKind = providercontract.ErrorTimeout
	if _, err := BinanceEvidence("outcome-result", now, observation, badOutcome, nil); err == nil {
		t.Fatal("observation/result outcome mismatch was accepted")
	}
	reversed := providercontract.Metadata{EventTime: timePointer(now), Source: providercontract.SourceRef{Key: "spot-ohlcv-1m", SourceID: "BTCUSDT:2:1"}}
	if err := validateOfficialSource(reversed, quality.SourceBinanceSpot, quality.CapabilityOHLCV); err == nil {
		t.Fatal("reversed OHLCV provenance range was accepted")
	}
}

func TestCoinGlassFixtureIsAlwaysRestrictedAndNotLive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	result := derivativeResult(now)
	evidence, err := CoinGlassEvidence("cg-oi-fixture-1", now, coinglass.Observation{
		Provider: coinglass.ProviderID, Operation: coinglass.OperationOpenInterestHistory,
		Capability: providercontract.CapabilityDerivatives, Outcome: "success", Duration: 10 * time.Millisecond,
	}, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Source != quality.SourceCoinGlassDerivative || evidence.Capability != quality.CapabilityOpenInterest || evidence.License != quality.LicenseRestricted || evidence.Live {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestResearchListIsOneFetchWithExactPriorityDistribution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	result := researchsignal.ListResult{Status: researchsignal.StatusLegacy, Data: researchsignal.EventList{Items: []researchsignal.Signal{
		researchItem("evt-1", now.Add(-time.Minute), "P0", []string{"legacy_fields_missing"}),
		researchItemWithFields("evt-2", now.Add(-2*time.Minute), "P1", stringPointer("watch"), nil),
		researchItemWithFields("evt-3", now.Add(-3*time.Minute), "P1", nil, stringPointer("invalidate")),
	}}, Quality: researchsignal.QualityStats{InputCount: 3, OutputCount: 3}}
	evidence, err := ResearchEventsEvidence("research-events-1", now, 20*time.Millisecond, result, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Priorities != (quality.PriorityCounts{P0: 1, P1: 2}) || evidence.Priority != "" || evidence.NoData {
		t.Fatalf("priority evidence=%+v", evidence)
	}
	if got := evidence.Metrics[quality.MetricResearchLegacy]; got != (quality.Counters{Numerator: 1, Denominator: 3}) {
		t.Fatalf("legacy=%+v", got)
	}
}

func TestResearchEmptyIsSuccessfulNoDataAndNeverApprovedByAdapter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	evidence, err := ResearchSummaryEvidence("research-summary-1", now, time.Millisecond, researchsignal.SummaryResult{Status: researchsignal.StatusEmpty}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Outcome != quality.OutcomeSuccess || !evidence.NoData || evidence.License != quality.LicenseUnknown || evidence.Priority != "" || evidence.Priorities != (quality.PriorityCounts{}) {
		t.Fatalf("evidence=%+v", evidence)
	}
	for _, metric := range []quality.Metric{quality.MetricCompleteness, quality.MetricConsistency, quality.MetricCoverage} {
		if got := evidence.Metrics[metric]; got != (quality.Counters{}) || got.BPS() != nil {
			t.Fatalf("empty metric %s=%+v", metric, got)
		}
	}
}

func TestResearchStatusAndPartialMetricsFailClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	stale, err := ResearchSummaryEvidence("summary-stale", now, time.Millisecond, researchsignal.SummaryResult{Status: researchsignal.StatusStale}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Outcome != quality.OutcomeStale || stale.Metrics != nil || len(stale.HardFaults) != 1 {
		t.Fatalf("stale=%+v", stale)
	}
	degraded, err := ResearchSummaryEvidence("summary-degraded", now, time.Millisecond, researchsignal.SummaryResult{Status: researchsignal.StatusDegraded}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if degraded.Outcome != quality.OutcomeUnconfigured || degraded.Metrics != nil {
		t.Fatalf("degraded=%+v", degraded)
	}
	if _, err := ResearchSummaryEvidence("summary-invalid", now, time.Millisecond, researchsignal.SummaryResult{}, nil, true); err == nil {
		t.Fatal("zero status was accepted")
	}

	first := researchItem("evt-b", now.Add(-2*time.Minute), "P1", nil)
	second := researchItem("evt-a", now.Add(-time.Minute), "P2", nil)
	partial, err := ResearchEventsEvidence("events-partial", now, time.Millisecond, researchsignal.ListResult{
		Status: researchsignal.StatusPartial, Data: researchsignal.EventList{Items: []researchsignal.Signal{first, second}},
		Quality: researchsignal.QualityStats{InputCount: 5, OutputCount: 2, DuplicateCount: 1, ConflictCount: 1},
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := partial.Metrics[quality.MetricDuplicate]; got != (quality.Counters{Numerator: 1, Denominator: 5}) {
		t.Fatalf("duplicate=%+v", got)
	}
	if got := partial.Metrics[quality.MetricConflict]; got != (quality.Counters{Numerator: 1, Denominator: 5}) {
		t.Fatalf("conflict=%+v", got)
	}
	if got := partial.Metrics[quality.MetricOutOfOrder]; got != (quality.Counters{Numerator: 1, Denominator: 2}) {
		t.Fatalf("order=%+v", got)
	}
	tie := now.Add(-time.Minute).Format(time.RFC3339Nano)
	ordered := []researchsignal.Signal{{ID: "evt-z", PublishedAt: tie, EventTime: now.Add(-10 * time.Hour).Format(time.RFC3339Nano)}, {ID: "evt-a", PublishedAt: tie, EventTime: now.Format(time.RFC3339Nano)}}
	if got := outOfOrderCount(ordered); got != 0 {
		t.Fatalf("published/id descending order falsely rejected: %d", got)
	}
}

func TestResearchSummarySourceCoverageIsExact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	evidence, err := ResearchSummaryEvidence("summary-sources", now, time.Millisecond, researchsignal.SummaryResult{
		Status: researchsignal.StatusFresh,
		Data:   researchsignal.Summary{Sources: []researchsignal.SourceStatus{{Source: "one", Status: researchsignal.SourceHealthy}, {Source: "two", Status: researchsignal.SourceDegraded}}},
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := evidence.Metrics[quality.MetricResearchSource]; got != (quality.Counters{Numerator: 1, Denominator: 2}) {
		t.Fatalf("source coverage=%+v", got)
	}
}

func TestResearchEvidenceRefsRemainDistinctAcrossRecoveryWindows(t *testing.T) {
	start := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	clock := &adapterClock{now: start}
	monitor, err := quality.NewMonitor(quality.DefaultPolicies(), clock)
	if err != nil {
		t.Fatal(err)
	}

	fault, err := ResearchSummaryEvidence("research-fault", start.Add(-time.Minute), time.Millisecond, researchsignal.SummaryResult{Status: researchsignal.StatusStale}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	fault.License = quality.LicenseApproved // controlled recovery fixture only
	if err := monitor.Record(fault); err != nil {
		t.Fatal(err)
	}
	set, err := monitor.Advance(start)
	if err != nil {
		t.Fatal(err)
	}
	if reportForSource(t, set, quality.SourceXiuqiuResearch).Gate.Status != quality.StatusQuarantined {
		t.Fatalf("fault did not quarantine research: %+v", reportForSource(t, set, quality.SourceXiuqiuResearch).Gate)
	}

	window := quality.DefaultPolicies()[quality.SourceXiuqiuResearch].WindowDuration
	for sequence := 1; sequence <= 3; sequence++ {
		end := start.Add(time.Duration(sequence) * window)
		clock.now = end
		summaryID := fmt.Sprintf("research-summary-recovery-%d", sequence)
		eventsID := fmt.Sprintf("research-events-recovery-%d", sequence)
		summary, err := ResearchSummaryEvidence(summaryID, end.Add(-2*time.Minute), time.Millisecond, researchsignal.SummaryResult{
			Status: researchsignal.StatusFresh,
			Data:   researchsignal.Summary{Sources: []researchsignal.SourceStatus{{Source: "xiuqiu-site Market Radar", Status: researchsignal.SourceHealthy}}},
		}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		item := researchItemWithFields("evt-recovery", end.Add(-3*time.Minute), "P1", stringPointer("watch"), stringPointer("invalidate"))
		events, err := ResearchEventsEvidence(eventsID, end.Add(-time.Minute), time.Millisecond, researchsignal.ListResult{
			Status:  researchsignal.StatusFresh,
			Data:    researchsignal.EventList{Items: []researchsignal.Signal{item}},
			Quality: researchsignal.QualityStats{InputCount: 1, OutputCount: 1},
		}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, evidence := range []*quality.Evidence{&summary, &events} {
			evidence.License = quality.LicenseApproved // approved property fixture, never production
			if err := monitor.Record(*evidence); err != nil {
				t.Fatal(err)
			}
		}
		set, err = monitor.Advance(end)
		if err != nil {
			t.Fatal(err)
		}
		report := reportForSource(t, set, quality.SourceXiuqiuResearch)
		if len(report.EvidenceRefs) != 2 || report.EvidenceRefs[0] == report.EvidenceRefs[1] {
			t.Fatalf("window %d refs collapsed: %+v", sequence, report.EvidenceRefs)
		}
		wantStatus := quality.StatusRecovering
		if sequence == 3 {
			wantStatus = quality.StatusHealthy
		}
		if report.Gate.Status != wantStatus || report.Gate.HealthyWindowStreak != uint32(sequence) {
			t.Fatalf("recovery window %d gate=%+v", sequence, report.Gate)
		}
		for _, ref := range report.EvidenceRefs {
			if !strings.Contains(ref.SourceID, fmt.Sprintf("recovery-%d", sequence)) {
				t.Fatalf("window %d ref lacks immutable fact ID: %+v", sequence, ref)
			}
		}
	}
}

type adapterClock struct{ now time.Time }

func (c *adapterClock) Now() time.Time { return c.now }

func reportForSource(t *testing.T, set quality.ReportSet, source quality.SourceKind) quality.Report {
	t.Helper()
	for _, report := range set.Reports {
		if report.Source == source {
			return report
		}
	}
	t.Fatalf("missing source report %s", source)
	return quality.Report{}
}

func stringPointer(value string) *string     { return &value }
func timePointer(value time.Time) *time.Time { return &value }

func researchItem(id string, eventTime time.Time, priority string, flags []string) researchsignal.Signal {
	return researchItemWithFields(id, eventTime, priority, nil, nil, flags...)
}

func researchItemWithFields(id string, eventTime time.Time, priority string, watch, invalidation *string, flags ...string) researchsignal.Signal {
	return researchsignal.Signal{
		ID: id, Source: "xiuqiu-site Market Radar", Provider: researchsignal.Provider,
		SourceURL: "https://xiuqiu-site.vercel.app/market-radar/events/" + id,
		EventTime: eventTime.UTC().Format(time.RFC3339Nano), Priority: priority, Freshness: researchsignal.FreshnessFresh,
		PublishedAt: eventTime.UTC().Format(time.RFC3339Nano),
		WatchFor:    watch, Invalidation: invalidation, QualityFlags: flags,
	}
}

func tickerResult(now time.Time, flags []providercontract.QualityFlag) providercontract.DispatchResult {
	eventTime := now.Add(-time.Second)
	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source:        providercontract.SourceRef{Provider: binancepublic.ProviderID, Key: "spot-ticker-24h", SourceID: "BTCUSDT:" + strconv.FormatInt(eventTime.UnixMilli(), 10)},
		Capability:    providercontract.CapabilitySpotTicker, ObservedAt: eventTime, EventTime: &eventTime, ReceivedAt: now, TTL: 5 * time.Second,
		Quality: flags,
	}
	value := providercontract.SpotTickerEnvelope{
		Meta:   meta,
		Market: providercontract.Market{ID: "market-btc-usdt", Venue: "binance", Base: providercontract.Asset{ID: "bitcoin", Symbol: "BTC"}, Quote: providercontract.Asset{ID: "tether", Symbol: "USDT"}, Type: providercontract.MarketTypeSpot},
		Data:   providercontract.SpotTicker{LastPrice: providercontract.DecimalValue{Value: "60000", Unit: providercontract.UnitQuoteAsset, Scale: 2}, ProviderSymbol: "BTCUSDT"},
	}
	return providercontract.DispatchResult{
		Response: providercontract.Response{Capability: providercontract.CapabilitySpotTicker, Value: value, Meta: meta},
		Trace:    providercontract.DispatchTrace{ActualProvider: binancepublic.ProviderID, Source: meta.Source, Attempts: []providercontract.AttemptTrace{{Provider: binancepublic.ProviderID, Capability: providercontract.CapabilitySpotTicker}}},
	}
}

func derivativeResult(now time.Time) providercontract.DispatchResult {
	eventTime := now.Add(-time.Hour)
	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source:        providercontract.SourceRef{Provider: coinglass.ProviderID, Key: "open-interest-history-4h", SourceID: "endpoint=/api/futures/open-interest/history;exchange=Binance;instrument=BTCUSD_PERP;settlement=USDT;time=" + strconv.FormatInt(eventTime.UnixMilli(), 10)},
		Capability:    providercontract.CapabilityDerivatives, ObservedAt: now, EventTime: &eventTime, ReceivedAt: now, TTL: 5 * time.Hour,
		Quality: []providercontract.QualityFlag{providercontract.QualityPartial},
	}
	openInterest := providercontract.DecimalValue{Value: "1000000", Unit: providercontract.UnitUSD, Scale: 2}
	value := providercontract.DerivativeSnapshotEnvelope{
		Meta:   meta,
		Market: providercontract.Market{ID: "market-btc-usd-perp", Venue: "binance", Base: providercontract.Asset{ID: "bitcoin", Symbol: "BTC"}, Quote: providercontract.Asset{ID: "usd", Symbol: "USD"}, Type: providercontract.MarketTypePerp},
		Data:   providercontract.DerivativeSnapshot{OpenInterest: &openInterest},
	}
	return providercontract.DispatchResult{
		Response: providercontract.Response{Capability: providercontract.CapabilityDerivatives, Value: value, Meta: meta},
		Trace:    providercontract.DispatchTrace{ActualProvider: coinglass.ProviderID, Source: meta.Source, Attempts: []providercontract.AttemptTrace{{Provider: coinglass.ProviderID, Capability: providercontract.CapabilityDerivatives}}},
	}
}
