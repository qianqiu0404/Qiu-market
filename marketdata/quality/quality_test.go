package quality

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestDefaultPoliciesMatchFrozenSLOs(t *testing.T) {
	policies := DefaultPolicies()
	if err := ValidatePolicies(policies); err != nil {
		t.Fatal(err)
	}
	binance := policies[SourceBinanceSpot]
	if binance.WindowDuration != 5*time.Minute || binance.CapabilityRules[CapabilitySpotTicker] != (CapabilityRule{5, 5 * time.Second}) || binance.CapabilityRules[CapabilityOHLCV] != (CapabilityRule{2, 65 * time.Second}) || binance.MaxLatency != 2*time.Second {
		t.Fatalf("binance SLO mismatch: %+v", binance)
	}
	coinGlass := policies[SourceCoinGlassDerivative]
	if coinGlass.WindowDuration != 5*time.Hour || coinGlass.MaxLatency != 5*time.Second || len(coinGlass.DeclaredCoverageGaps) != 1 || coinGlass.DeclaredCoverageGaps[0] != CapabilityFunding {
		t.Fatalf("CoinGlass SLO mismatch: %+v", coinGlass)
	}
	research := policies[SourceXiuqiuResearch]
	if research.WindowDuration != 168*time.Hour || research.MinSamples != 2 || research.MaxLatency != 5*time.Second {
		t.Fatalf("research SLO mismatch: %+v", research)
	}
}

func TestEvaluateExactGoodBinanceDimensions(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	evidence := make([]Evidence, 0, 7)
	for i := 0; i < 5; i++ {
		evidence = append(evidence, goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Duration(i+1)*time.Second), "ticker-"+string(rune('a'+i))))
	}
	for i := 0; i < 2; i++ {
		evidence = append(evidence, goodEvidence(SourceBinanceSpot, CapabilityOHLCV, end.Add(-time.Duration(i+1)*time.Second), "ohlcv-"+string(rune('a'+i))))
	}
	report := evaluateFixture(t, DefaultPolicies()[SourceBinanceSpot], end, evidence)
	if report.Status != StatusHealthy || report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != BasisPoints || report.Grade == nil || *report.Grade != GradeA {
		t.Fatalf("good report=%+v", report)
	}
	if report.AttemptCount != 7 || report.SuccessCount != 7 || !report.PublicEligible || report.TradeEligible || report.ReferenceEligible || report.MatcherEligible || report.LedgerEligible {
		t.Fatalf("eligibility/count mismatch: %+v", report)
	}
	assertDimension(t, report, MetricAvailability, 7, 7, BasisPoints)
	assertDimension(t, report, MetricFreshness, 7, 7, BasisPoints)
	assertDimension(t, report, MetricCompleteness, 70, 70, BasisPoints)
	assertDimension(t, report, MetricCoverage, 2, 2, BasisPoints)
}

func TestEvaluateInsufficientEmptyAndPartial(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	policy := DefaultPolicies()[SourceBinanceSpot]
	empty := evaluateFixture(t, policy, end, nil)
	if empty.Status != StatusInsufficient || empty.TechnicalScoreBPS != nil || empty.Grade != nil || empty.PublicEligible {
		t.Fatalf("empty=%+v", empty)
	}
	partial := []Evidence{goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Second), "ticker-one")}
	report := evaluateFixture(t, policy, end, partial)
	if report.Status != StatusInsufficient || report.TechnicalScoreBPS != nil {
		t.Fatalf("partial=%+v", report)
	}
	if report.Capabilities[0].ValidSampleCount != 1 || report.Capabilities[1].ValidSampleCount != 0 {
		t.Fatalf("capability counts=%+v", report.Capabilities)
	}
}

func TestEvaluateQualityFaultAndFailureMatrix(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	base := perfectBinance(end)
	tests := []struct {
		name      string
		mutate    func([]Evidence)
		status    Status
		metric    Metric
		numerator uint64
	}{
		{"duplicate", func(items []Evidence) { items[0].Metrics[MetricDuplicate] = Counters{1, 1} }, StatusDegraded, MetricDuplicate, 1},
		{"out_of_order", func(items []Evidence) { items[0].Metrics[MetricOutOfOrder] = Counters{1, 1} }, StatusDegraded, MetricOutOfOrder, 1},
		{"stale", func(items []Evidence) { items[0].Metrics[MetricStale] = Counters{1, 1} }, StatusQuarantined, MetricStale, 1},
		{"future", func(items []Evidence) { items[0].Metrics[MetricFuture] = Counters{1, 1} }, StatusQuarantined, MetricFuture, 1},
		{"conflict", func(items []Evidence) { items[0].Metrics[MetricConflict] = Counters{1, 1} }, StatusQuarantined, MetricConflict, 1},
		{"rate_limit", func(items []Evidence) { items[0].Outcome = OutcomeRateLimit; items[0].Metrics = nil }, StatusInsufficient, MetricRateLimit, 1},
		{"upstream_5xx", func(items []Evidence) { items[0].Outcome = OutcomeUpstream5xx; items[0].Metrics = nil }, StatusInsufficient, MetricUpstream5xx, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := cloneEvidenceSlice(base)
			test.mutate(items)
			report := evaluateFixture(t, DefaultPolicies()[SourceBinanceSpot], end, items)
			if report.Status != test.status {
				t.Fatalf("status=%s want=%s reasons=%v", report.Status, test.status, report.Reasons)
			}
			dimension := findDimension(t, report, test.metric)
			if dimension.Numerator != test.numerator {
				t.Fatalf("dimension=%+v", dimension)
			}
		})
	}
}

func TestEvaluateLicenseCacheAndCoinGlassCoverage(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	unknown := perfectBinance(end)
	for i := range unknown {
		unknown[i].License = LicenseUnknown
	}
	report := evaluateFixture(t, DefaultPolicies()[SourceBinanceSpot], end, unknown)
	if report.Status != StatusQuarantined || report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != BasisPoints || report.PublicEligible {
		t.Fatalf("unknown license=%+v", report)
	}
	cache := goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Second), "cache")
	cache.CacheHit = true
	cacheReport := evaluateFixture(t, DefaultPolicies()[SourceBinanceSpot], end, []Evidence{cache})
	if cacheReport.AttemptCount != 0 || cacheReport.SuccessCount != 0 || cacheReport.CacheHitCount != 1 || cacheReport.Status != StatusInsufficient {
		t.Fatalf("cache evidence impersonated upstream: %+v", cacheReport)
	}
	coinGlass := []Evidence{goodEvidence(SourceCoinGlassDerivative, CapabilityOpenInterest, end.Add(-time.Second), "oi"), goodEvidence(SourceCoinGlassDerivative, CapabilityLiquidation, end.Add(-2*time.Second), "liq")}
	funding := goodEvidence(SourceCoinGlassDerivative, CapabilityFunding, end.Add(-3*time.Second), "funding")
	funding.Outcome = OutcomeUnsupported
	funding.Metrics = nil
	coinGlass = append(coinGlass, funding)
	cgReport := evaluateFixture(t, DefaultPolicies()[SourceCoinGlassDerivative], end, coinGlass)
	if cgReport.AttemptCount != 2 || cgReport.SuccessCount != 2 || len(cgReport.CoverageGaps) != 1 || cgReport.CoverageGaps[0] != CapabilityFunding {
		t.Fatalf("CoinGlass denominator/gap=%+v", cgReport)
	}
	assertDimension(t, cgReport, MetricAvailability, 2, 2, BasisPoints)
}

func TestEvaluateResearchPriorityAndNoData(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	summary := goodEvidence(SourceXiuqiuResearch, CapabilityResearchSummary, end.Add(-time.Second), "summary")
	event := goodEvidence(SourceXiuqiuResearch, CapabilityResearchEvents, end.Add(-2*time.Second), "event")
	event.Priority = PriorityP0
	report := evaluateFixture(t, DefaultPolicies()[SourceXiuqiuResearch], end, []Evidence{summary, event})
	if report.PriorityCounts != (PriorityCounts{P0: 1}) || !report.ResearchContextEligible {
		t.Fatalf("research report=%+v", report)
	}
	emptyEvent := event
	emptyEvent.ID = "empty-event"
	emptyEvent.Ref.SourceID = "fixture:empty-event"
	emptyEvent.NoData = true
	emptyEvent.Priority = ""
	noData := evaluateFixture(t, DefaultPolicies()[SourceXiuqiuResearch], end, []Evidence{summary, emptyEvent})
	if noData.Status != StatusInsufficient || noData.TechnicalScoreBPS != nil || noData.Capabilities[1].ValidSampleCount != 0 {
		t.Fatalf("verified empty must be no_data: %+v", noData)
	}
	batch := event
	batch.ID, batch.Ref.SourceID, batch.Priority = "event-batch", "fixture:event-batch", ""
	batch.Priorities = PriorityCounts{P0: 2, P1: 3, P2: 4}
	batchReport := evaluateFixture(t, DefaultPolicies()[SourceXiuqiuResearch], end, []Evidence{summary, batch})
	if batchReport.PriorityCounts != batch.Priorities || batchReport.AttemptCount != 2 {
		t.Fatalf("batch priority inflated attempts: %+v", batchReport)
	}
}

func TestResearchCoverageIncludesProvenanceWatchAndInvalidation(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	summary := goodEvidence(SourceXiuqiuResearch, CapabilityResearchSummary, end.Add(-time.Second), "context-summary")
	event := goodEvidence(SourceXiuqiuResearch, CapabilityResearchEvents, end.Add(-2*time.Second), "context-event")
	summary.Metrics[MetricResearchSource] = Counters{Numerator: 4, Denominator: 5}
	event.Metrics[MetricResearchSource] = Counters{Numerator: 1, Denominator: 1}
	event.Metrics[MetricResearchWatch] = Counters{Denominator: 1}
	event.Metrics[MetricResearchInvalidation] = Counters{Denominator: 1}
	report := evaluateFixture(t, DefaultPolicies()[SourceXiuqiuResearch], end, []Evidence{summary, event})
	assertDimension(t, report, MetricCoverage, 7, 10, 7000)
	if report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != 9250 || report.Grade == nil || *report.Grade != GradeA || report.Status != StatusDegraded || report.PublicEligible {
		t.Fatalf("research context score=%+v", report)
	}
	delete(event.Metrics, MetricResearchWatch)
	missing := evaluateFixture(t, DefaultPolicies()[SourceXiuqiuResearch], end, []Evidence{summary, event})
	if missing.Status != StatusInsufficient || missing.TechnicalScoreBPS != nil || missing.Grade != nil {
		t.Fatalf("missing research context denominator=%+v", missing)
	}
}

func TestPriorityRepresentationsAreExclusiveAndImmutable(t *testing.T) {
	at := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	evidence := goodEvidence(SourceXiuqiuResearch, CapabilityResearchEvents, at.Add(-time.Second), "priority-contract")
	evidence.Priority = PriorityP0
	evidence.Priorities = PriorityCounts{P1: 1}
	if err := ValidateEvidence(evidence); err == nil {
		t.Fatal("singular and batch priority must be mutually exclusive")
	}
	evidence.Priority = ""
	monitor, err := NewMonitor(DefaultPolicies(), fixedClock{at})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Record(evidence); err != nil {
		t.Fatal(err)
	}
	changed := evidence
	changed.Priorities = PriorityCounts{P2: 1}
	if err := monitor.Record(changed); err == nil {
		t.Fatal("same ID priority mutation must conflict")
	}
}

func TestHardInvalidEvidenceDoesNotIncreaseCapabilityValidSamples(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	items := perfectBinance(end)
	items[5].HardFaults = []HardFault{HardFaultFuture}
	items[5].Metrics[MetricFuture] = Counters{Numerator: 1, Denominator: 1}
	report := evaluateFixture(t, DefaultPolicies()[SourceBinanceSpot], end, items)
	ohlcv := report.Capabilities[1]
	if ohlcv.Capability != CapabilityOHLCV || ohlcv.ValidSampleCount != 1 || ohlcv.Status != StatusQuarantined || report.Status != StatusQuarantined {
		t.Fatalf("hard invalid capability=%+v source=%s", ohlcv, report.Status)
	}
}

func TestGateRequiresThreeHealthyWindowsAfterQuarantine(t *testing.T) {
	policy := DefaultPolicies()[SourceBinanceSpot]
	gate, err := NewGate(policy)
	if err != nil {
		t.Fatal(err)
	}
	healthy := Report{Source: policy.Source, Status: StatusHealthy, AttemptCount: 7}
	fault := Report{Source: policy.Source, Status: StatusQuarantined, AttemptCount: 7, Reasons: []string{"hard_fault:future"}}
	if state := gate.Advance(fault); state.Status != StatusQuarantined || state.HealthyWindowStreak != 0 {
		t.Fatal(state)
	}
	for want := uint32(1); want <= 2; want++ {
		state := gate.Advance(healthy)
		if state.Status != StatusRecovering || state.HealthyWindowStreak != want {
			t.Fatalf("recovery %d=%+v", want, state)
		}
	}
	state := gate.Advance(healthy)
	if state.Status != StatusHealthy || state.HealthyWindowStreak != 3 {
		t.Fatal(state)
	}
	gate.Advance(fault)
	gate.Advance(healthy)
	empty := Report{Source: policy.Source, Status: StatusInsufficient}
	if state := gate.Advance(empty); state.Status != StatusQuarantined || state.HealthyWindowStreak != 0 {
		t.Fatalf("empty advanced recovery: %+v", state)
	}
}

func TestGateRetainsOnlyCurrentWindowEvidenceRefs(t *testing.T) {
	policy := DefaultPolicies()[SourceBinanceSpot]
	gate, err := NewGate(policy)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	for window := 0; window < 100; window++ {
		windowEnd := end.Add(time.Duration(window+1) * policy.WindowDuration)
		report := Report{Source: policy.Source, Status: StatusHealthy, Window: EvidenceWindow{Start: windowEnd.Add(-policy.WindowDuration), End: windowEnd, Duration: policy.WindowDuration}, EvidenceRefs: []EvidenceRef{{SourceID: fmt.Sprintf("fixture:window-%03d", window)}}}
		gate.Advance(report)
		if len(gate.seenRefs) != 1 {
			t.Fatalf("window %d retained %d refs", window, len(gate.seenRefs))
		}
	}
}

func TestMonitorStableIndependentReportsAndIdempotency(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	monitor, err := NewMonitor(DefaultPolicies(), fixedClock{end})
	if err != nil {
		t.Fatal(err)
	}
	evidence := goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Second), "stable")
	if err := monitor.Record(evidence); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Record(evidence); err != nil {
		t.Fatal(err)
	}
	conflict := evidence
	conflict.Outcome = OutcomeTimeout
	if err := monitor.Record(conflict); err == nil {
		t.Fatal("same evidence ID with different fact must conflict")
	}
	set, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reports) != 3 || set.Reports[0].Source != SourceBinanceSpot || set.Reports[1].Source != SourceCoinGlassDerivative || set.Reports[2].Source != SourceXiuqiuResearch {
		t.Fatalf("unstable report order: %+v", set.Reports)
	}
	if set.Reports[0].AttemptCount != 1 {
		t.Fatalf("idempotent replay inflated denominator: %+v", set.Reports[0])
	}
}

func TestMonitorInitialReportsAreInternallyConsistentWithoutAdvancingGate(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	monitor, err := NewMonitor(DefaultPolicies(), fixedClock{end})
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range perfectBinance(end) {
		if err := monitor.Record(evidence); err != nil {
			t.Fatal(err)
		}
	}
	set, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	report := reportFor(t, set, SourceBinanceSpot)
	if report.Status != StatusHealthy || report.Gate.Status != StatusHealthy || report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != BasisPoints {
		t.Fatalf("initial read invariant=%+v", report)
	}
	second, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	if reportFor(t, second, SourceBinanceSpot).Gate.HealthyWindowStreak != 0 {
		t.Fatal("read-only Reports advanced gate")
	}
}

func TestMonitorAdvanceIsIdempotentAndRequiresNewEvidence(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: end}
	monitor, err := NewMonitor(DefaultPolicies(), clock)
	if err != nil {
		t.Fatal(err)
	}
	recordWindow := func(windowEnd time.Time, prefix string, fault bool) {
		items := perfectBinance(windowEnd)
		for i := range items {
			items[i].ID = prefix + "-" + items[i].ID
			items[i].Ref.SourceID = "fixture:" + items[i].ID
		}
		if fault {
			items[0].Metrics[MetricFuture] = Counters{Numerator: 1, Denominator: 1}
		}
		for _, item := range items {
			if err := monitor.Record(item); err != nil {
				t.Fatal(err)
			}
		}
	}
	recordWindow(end, "fault", true)
	set, err := monitor.Advance(end)
	if err != nil {
		t.Fatal(err)
	}
	if reportFor(t, set, SourceBinanceSpot).Status != StatusQuarantined {
		t.Fatal(reportFor(t, set, SourceBinanceSpot))
	}
	end = end.Add(time.Minute)
	clock.now = end
	replayOld, err := monitor.Advance(end)
	if err != nil {
		t.Fatal(err)
	}
	if got := reportFor(t, replayOld, SourceBinanceSpot).Gate.HealthyWindowStreak; got != 0 {
		t.Fatalf("old evidence advanced recovery: %d", got)
	}
	end = end.Add(5 * time.Minute)
	clock.now = end
	cacheItems := perfectBinance(end)
	for i := range cacheItems {
		cacheItems[i].ID = "cache-" + cacheItems[i].ID
		cacheItems[i].Ref.SourceID = "fixture:" + cacheItems[i].ID
		cacheItems[i].CacheHit = true
		if err := monitor.Record(cacheItems[i]); err != nil {
			t.Fatal(err)
		}
	}
	cacheSet, err := monitor.Advance(end)
	if err != nil {
		t.Fatal(err)
	}
	if got := reportFor(t, cacheSet, SourceBinanceSpot).Gate.HealthyWindowStreak; got != 0 {
		t.Fatalf("cache hits advanced recovery: %d", got)
	}
	for window := 1; window <= 3; window++ {
		end = end.Add(5 * time.Minute)
		clock.now = end
		recordWindow(end, "good"+string(rune('a'+window)), false)
		set, err = monitor.Advance(end)
		if err != nil {
			t.Fatal(err)
		}
		report := reportFor(t, set, SourceBinanceSpot)
		if window < 3 && (report.Status != StatusRecovering || report.Gate.HealthyWindowStreak != uint32(window)) {
			t.Fatalf("window %d=%+v", window, report.Gate)
		}
		if window == 3 && (report.Status != StatusHealthy || report.Gate.HealthyWindowStreak != 3) {
			t.Fatalf("window 3=%+v", report.Gate)
		}
		replay, replayErr := monitor.Advance(end)
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if got := reportFor(t, replay, SourceBinanceSpot).Gate.HealthyWindowStreak; got != report.Gate.HealthyWindowStreak {
			t.Fatalf("same window replay advanced streak: %d -> %d", report.Gate.HealthyWindowStreak, got)
		}
		readOnly, readErr := monitor.Reports()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got := reportFor(t, readOnly, SourceBinanceSpot).Gate.HealthyWindowStreak; got != report.Gate.HealthyWindowStreak {
			t.Fatalf("Reports advanced streak: %d -> %d", report.Gate.HealthyWindowStreak, got)
		}
	}
	if _, err := monitor.Advance(end.Add(-time.Second)); err == nil {
		t.Fatal("non-monotonic window must fail closed")
	}
}

func TestMonitorEvidenceCapacityFailsClosedAndPrunesSeenIDs(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: end}
	monitor, err := NewMonitor(DefaultPolicies(), clock)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxEvidencePerSource; i++ {
		id := fmt.Sprintf("bounded-%04d", i)
		evidence := goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Second), id)
		if err := monitor.Record(evidence); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	overflow := goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Second), "bounded-overflow")
	if err := monitor.Record(overflow); !errors.Is(err, ErrEvidenceLimit) {
		t.Fatalf("capacity err=%v", err)
	}
	if _, err := monitor.Advance(end); err != nil {
		t.Fatal(err)
	}
	if len(monitor.evaluatedIDs[SourceBinanceSpot]) != maxEvidencePerSource {
		t.Fatalf("seen=%d", len(monitor.evaluatedIDs[SourceBinanceSpot]))
	}
	clock.now = end.Add(6 * time.Minute)
	fresh := goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, clock.now.Add(-time.Second), "bounded-fresh")
	if err := monitor.Record(fresh); err != nil {
		t.Fatal(err)
	}
	if len(monitor.evidence[SourceBinanceSpot]) != 1 || len(monitor.evaluatedIDs[SourceBinanceSpot]) != 0 {
		t.Fatalf("retention evidence=%d seen=%d", len(monitor.evidence[SourceBinanceSpot]), len(monitor.evaluatedIDs[SourceBinanceSpot]))
	}
}

func TestEvaluateRejectsCounterOverflow(t *testing.T) {
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	items := perfectBinance(end)
	for i := range items {
		items[i].Metrics[MetricCompleteness] = Counters{math.MaxUint64, math.MaxUint64}
	}
	window, err := NewEvidenceWindow(end.Add(-5*time.Minute), end, 2, items)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Evaluate(DefaultPolicies()[SourceBinanceSpot], window); err == nil {
		t.Fatal("counter overflow must fail closed")
	}
}

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

type mutableClock struct{ now time.Time }

func (f *mutableClock) Now() time.Time { return f.now }
func goodEvidence(source SourceKind, capability Capability, at time.Time, id string) Evidence {
	metrics := map[Metric]Counters{MetricFreshness: {1, 1}, MetricCompleteness: {10, 10}}
	if source == SourceXiuqiuResearch {
		metrics[MetricResearchSource] = Counters{Numerator: 1, Denominator: 1}
		if capability == CapabilityResearchEvents {
			metrics[MetricResearchWatch] = Counters{Numerator: 1, Denominator: 1}
			metrics[MetricResearchInvalidation] = Counters{Numerator: 1, Denominator: 1}
		}
	}
	return Evidence{ID: id, Source: source, Capability: capability, At: at, Outcome: OutcomeSuccess, Latency: time.Second, License: LicenseApproved, Live: true, Metrics: metrics, Ref: EvidenceRef{SourceID: "fixture:" + id}}
}
func perfectBinance(end time.Time) []Evidence {
	items := make([]Evidence, 0, 7)
	for i := 0; i < 5; i++ {
		items = append(items, goodEvidence(SourceBinanceSpot, CapabilitySpotTicker, end.Add(-time.Duration(i+1)*time.Second), "ticker-"+string(rune('a'+i))))
	}
	for i := 0; i < 2; i++ {
		items = append(items, goodEvidence(SourceBinanceSpot, CapabilityOHLCV, end.Add(-time.Duration(i+1)*time.Second), "ohlcv-"+string(rune('a'+i))))
	}
	return items
}
func cloneEvidenceSlice(in []Evidence) []Evidence {
	out := make([]Evidence, len(in))
	for i := range in {
		out[i] = cloneEvidence(in[i])
	}
	return out
}
func evaluateFixture(t *testing.T, policy Policy, end time.Time, evidence []Evidence) Report {
	t.Helper()
	window, err := NewEvidenceWindow(end.Add(-policy.WindowDuration), end, policy.MinSamples, evidence)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	return report
}
func findDimension(t *testing.T, report Report, metric Metric) Dimension {
	t.Helper()
	for _, dimension := range report.Dimensions {
		if dimension.Metric == metric {
			return dimension
		}
	}
	t.Fatalf("missing dimension %s", metric)
	return Dimension{}
}
func assertDimension(t *testing.T, report Report, metric Metric, numerator, denominator uint64, bps uint32) {
	t.Helper()
	got := findDimension(t, report, metric)
	if got.Numerator != numerator || got.Denominator != denominator || got.BPS == nil || *got.BPS != bps {
		t.Fatalf("%s=%+v", metric, got)
	}
}

func reportFor(t *testing.T, set ReportSet, source SourceKind) Report {
	t.Helper()
	for _, report := range set.Reports {
		if report.Source == source {
			return report
		}
	}
	t.Fatalf("missing report %s", source)
	return Report{}
}
