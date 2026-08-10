package quality

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestIndependentCountersBPSIsExactAtUint64Boundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Counters
		want uint32
	}{
		{name: "perfect", in: Counters{Numerator: math.MaxUint64, Denominator: math.MaxUint64}, want: BasisPoints},
		{name: "half", in: Counters{Numerator: math.MaxUint64 / 2, Denominator: math.MaxUint64}, want: BasisPoints / 2},
		{name: "one third rounds", in: Counters{Numerator: math.MaxUint64 / 3, Denominator: math.MaxUint64}, want: 3333},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := test.in.BPS()
			if got == nil || *got != test.want {
				t.Fatalf("BPS(%+v)=%v, want %d without overflow", test.in, got, test.want)
			}
		})
	}
	if got := (Counters{}).BPS(); got != nil {
		t.Fatalf("zero denominator must remain unknown, got %d", *got)
	}
}

func TestIndependentWindowRejectsNonUTCAndClonesNestedEvidence(t *testing.T) {
	t.Parallel()
	utcStart := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	local := time.FixedZone("non-utc-fixture", 8*60*60)
	localStart := utcStart.In(local)
	localEnd := utcStart.Add(time.Minute).In(local)
	localEvidence := Evidence{
		ID: "local-time", Source: SourceBinanceSpot, Capability: CapabilitySpotTicker,
		At: localStart.Add(time.Second), Outcome: OutcomeSuccess, License: LicenseApproved,
		Metrics: map[Metric]Counters{MetricFreshness: {Numerator: 1, Denominator: 1}},
		Ref:     EvidenceRef{SourceID: "binance-public:ticker:BTCUSDT"},
	}
	direct := EvidenceWindow{
		Start: localStart, End: localEnd, Duration: time.Minute,
		SampleCount: 1, MinSamples: 1, Evidence: []Evidence{localEvidence},
	}
	if err := direct.Validate(); err == nil {
		t.Fatal("non-UTC window and evidence time must fail closed")
	}

	metrics := map[Metric]Counters{MetricFreshness: {Numerator: 1, Denominator: 1}}
	faults := []HardFault{HardFaultFuture}
	input := []Evidence{{
		ID: "immutable", Source: SourceBinanceSpot, Capability: CapabilitySpotTicker,
		At: utcStart.Add(time.Second), Outcome: OutcomeSuccess, License: LicenseApproved,
		Metrics: metrics, HardFaults: faults,
		Ref: EvidenceRef{SourceID: "binance-public:ticker:BTCUSDT", ContentHash: "sha256:fixture"},
	}}
	window, err := NewEvidenceWindow(utcStart, utcStart.Add(time.Minute), 1, input)
	if err != nil {
		t.Fatal(err)
	}
	metrics[MetricFreshness] = Counters{Numerator: 0, Denominator: 1}
	faults[0] = HardFaultSchema
	input[0].Ref.SourceID = "mutated"
	got := window.Evidence[0]
	if got.Metrics[MetricFreshness].Numerator != 1 || got.HardFaults[0] != HardFaultFuture || got.Ref.SourceID != "binance-public:ticker:BTCUSDT" {
		t.Fatalf("window aliases caller-owned evidence: %+v", got)
	}
}

func TestIndependentEvaluatePerfectBinanceIsExactlyA(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	window := independentPerfectBinanceWindow(t, policy)
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	if report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != BasisPoints {
		t.Fatalf("perfect technical score=%v, want %d", report.TechnicalScoreBPS, BasisPoints)
	}
	if report.Grade == nil || *report.Grade != GradeA || report.Status != StatusHealthy {
		t.Fatalf("perfect grade/status=%v/%s, want A/healthy", report.Grade, report.Status)
	}
	if !report.PublicEligible || report.TradeEligible || report.ReferenceEligible || report.MatcherEligible || report.LedgerEligible {
		t.Fatalf("unsafe eligibility for perfect diagnostic report: %+v", report)
	}
	if len(report.Capabilities) != 2 {
		t.Fatalf("capability reports=%d, want 2 independent capabilities", len(report.Capabilities))
	}
	for _, capability := range report.Capabilities {
		if capability.Status != StatusHealthy || capability.ValidSampleCount < capability.MinSamples {
			t.Fatalf("capability not independently healthy: %+v", capability)
		}
	}
	if got := independentWeightedScore(t, policy, report.Dimensions); got != BasisPoints {
		t.Fatalf("independently recomputed score=%d, want %d", got, BasisPoints)
	}
	for _, metric := range []Metric{MetricFreshness, MetricAvailability, MetricCompleteness, MetricSchema, MetricConsistency, MetricCoverage} {
		dimension := independentDimension(t, report, metric)
		if dimension.BPS == nil || *dimension.BPS != BasisPoints {
			t.Fatalf("perfect %s=%+v, want 10000 BPS", metric, dimension)
		}
	}
}

func TestIndependentEvaluateEmptyIsInsufficientWithoutInventedScore(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	end := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	window, err := NewEvidenceWindow(end.Add(-policy.WindowDuration), end, policy.MinSamples, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusInsufficient || report.TechnicalScoreBPS != nil || report.Grade != nil {
		t.Fatalf("empty report=%+v, want insufficient with nil score/grade", report)
	}
	if report.PublicEligible || report.TradeEligible || report.ReferenceEligible || report.MatcherEligible || report.LedgerEligible {
		t.Fatalf("empty report has unsafe eligibility: %+v", report)
	}
	if len(report.EvidenceRefs) != 0 {
		t.Fatalf("empty report invented evidence refs: %+v", report.EvidenceRefs)
	}
}

func TestIndependentEvaluateRejectsCounterAggregationOverflow(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	window := independentPerfectBinanceWindow(t, policy)
	for index := range window.Evidence {
		window.Evidence[index].Metrics[MetricFreshness] = Counters{
			Numerator:   math.MaxUint64,
			Denominator: math.MaxUint64,
		}
	}
	if _, err := Evaluate(policy, window); err == nil {
		t.Fatal("overflowing aggregate counters must fail closed, not wrap into a plausible score")
	}
}

func TestIndependentReportsRemainSeparateAcrossAllThreeSourceClasses(t *testing.T) {
	t.Parallel()
	policies := DefaultPolicies()
	for _, source := range []SourceKind{SourceBinanceSpot, SourceCoinGlassDerivative, SourceXiuqiuResearch} {
		policy := policies[source]
		window := independentPerfectWindow(t, policy)
		report, err := Evaluate(policy, window)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if report.Source != source || report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != BasisPoints || report.Status != StatusHealthy {
			t.Fatalf("%s report was merged or rescored: %+v", source, report)
		}
		if got := independentWeightedScore(t, policy, report.Dimensions); got != BasisPoints {
			t.Fatalf("%s independent score=%d, want %d", source, got, BasisPoints)
		}
	}
}

func TestIndependentResearchContextGapsCannotScorePerfect(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceXiuqiuResearch]
	window := independentPerfectWindow(t, policy)
	for index := range window.Evidence {
		switch window.Evidence[index].Capability {
		case CapabilityResearchSummary:
			window.Evidence[index].Metrics[MetricResearchSource] = Counters{Numerator: 4, Denominator: 5}
		case CapabilityResearchEvents:
			window.Evidence[index].Metrics[MetricResearchSource] = Counters{Numerator: 1, Denominator: 1}
			window.Evidence[index].Metrics[MetricResearchWatch] = Counters{Denominator: 1}
			window.Evidence[index].Metrics[MetricResearchInvalidation] = Counters{Denominator: 1}
			window.Evidence[index].Metrics[MetricResearchPriority] = Counters{Numerator: 1, Denominator: 1}
		}
	}
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	coverage := independentDimension(t, report, MetricCoverage)
	if coverage.Numerator != 7 || coverage.Denominator != 10 || coverage.BPS == nil || *coverage.BPS != 7000 {
		t.Fatalf("research context coverage=%+v, want exact 7/10=7000", coverage)
	}
	if report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != 9250 || report.Status != StatusDegraded || report.PublicEligible {
		t.Fatalf("missing research provenance/watch/invalidation scored as perfect and healthy: %+v", report)
	}
}

func TestIndependentRateLimitAndUpstreamFailureUseAttemptDenominator(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	window := independentPerfectBinanceWindow(t, policy)
	window.Evidence = append(window.Evidence,
		independentFailureEvidence(window.End.Add(-500*time.Millisecond), "rate-limit", OutcomeRateLimit),
		independentFailureEvidence(window.End.Add(-400*time.Millisecond), "upstream-5xx", OutcomeUpstream5xx),
	)
	window.SampleCount = uint64(len(window.Evidence))
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	availability := independentDimension(t, report, MetricAvailability)
	if availability.Numerator != 7 || availability.Denominator != 9 || availability.BPS == nil || *availability.BPS != 7778 {
		t.Fatalf("availability=%+v, want exact 7/9=7778", availability)
	}
	if report.AttemptCount != 9 || report.SuccessCount != 7 || report.ErrorCounts[OutcomeRateLimit] != 1 || report.ErrorCounts[OutcomeUpstream5xx] != 1 {
		t.Fatalf("attempt/error accounting is wrong: attempts=%d success=%d errors=%v", report.AttemptCount, report.SuccessCount, report.ErrorCounts)
	}
	if report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != 9556 || *report.TechnicalScoreBPS != independentWeightedScore(t, policy, report.Dimensions) || report.Status != StatusDegraded {
		t.Fatalf("failure score/status not independently reproducible: %+v", report)
	}
}

func TestIndependentPartialCapabilityHasNoSourceScore(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	window := independentPerfectBinanceWindow(t, policy)
	for index := range window.Evidence {
		if window.Evidence[index].Capability == CapabilityOHLCV {
			window.Evidence = append(window.Evidence[:index], window.Evidence[index+1:]...)
			break
		}
	}
	window.SampleCount = uint64(len(window.Evidence))
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	coverage := independentDimension(t, report, MetricCoverage)
	if coverage.Numerator != 1 || coverage.Denominator != 2 || coverage.BPS == nil || *coverage.BPS != 5000 {
		t.Fatalf("partial capability coverage=%+v, want exact 1/2=5000", coverage)
	}
	if report.Status != StatusInsufficient || report.TechnicalScoreBPS != nil || report.Grade != nil || report.PublicEligible {
		t.Fatalf("partial capability inherited a plausible source score: %+v", report)
	}
}

func TestIndependentConsistencySignalsCannotRemainPerfect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		metric     Metric
		hardFault  HardFault
		wantStatus Status
	}{
		{name: "duplicate", metric: MetricDuplicate, wantStatus: StatusDegraded},
		{name: "out-of-order", metric: MetricOutOfOrder, wantStatus: StatusDegraded},
		{name: "conflict", metric: MetricConflict, hardFault: HardFaultConflict, wantStatus: StatusQuarantined},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy := DefaultPolicies()[SourceBinanceSpot]
			window := independentPerfectBinanceWindow(t, policy)
			affected := &window.Evidence[0]
			delete(affected.Metrics, MetricConsistency)
			affected.Metrics[test.metric] = Counters{Numerator: 1, Denominator: 1}
			if test.hardFault != "" {
				affected.HardFaults = []HardFault{test.hardFault}
			}
			report, err := Evaluate(policy, window)
			if err != nil {
				t.Fatal(err)
			}
			consistency := independentDimension(t, report, MetricConsistency)
			if consistency.Numerator != 6 || consistency.Denominator != 7 || consistency.BPS == nil || *consistency.BPS != 8571 {
				t.Fatalf("consistency=%+v, want exact 6/7=8571", consistency)
			}
			if report.TechnicalScoreBPS == nil || *report.TechnicalScoreBPS != 9857 || report.Status != test.wantStatus {
				t.Fatalf("%s report escaped quality penalty: %+v", test.name, report)
			}
			if report.TradeEligible || report.ReferenceEligible || report.MatcherEligible || report.LedgerEligible {
				t.Fatalf("%s evidence leaked into trading eligibility: %+v", test.name, report)
			}
		})
	}
}

func TestIndependentFutureStaleAndUnknownLicenseFailClosedButRetainEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*EvidenceWindow)
		reason string
	}{
		{name: "future", mutate: func(window *EvidenceWindow) {
			window.Evidence[0].HardFaults = []HardFault{HardFaultFuture}
			window.Evidence[0].Metrics[MetricFuture] = Counters{Numerator: 1, Denominator: 1}
		}, reason: "hard_fault:future"},
		{name: "stale", mutate: func(window *EvidenceWindow) {
			for index := range window.Evidence {
				if window.Evidence[index].Capability == CapabilitySpotTicker {
					window.Evidence[index].At = window.End.Add(-6*time.Second - time.Duration(index)*time.Millisecond)
				}
			}
		}, reason: "hard_fault:stale"},
		{name: "license-unknown", mutate: func(window *EvidenceWindow) {
			for index := range window.Evidence {
				window.Evidence[index].License = LicenseUnknown
			}
		}, reason: "license_unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy := DefaultPolicies()[SourceBinanceSpot]
			window := independentPerfectBinanceWindow(t, policy)
			test.mutate(&window)
			report, err := Evaluate(policy, window)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != StatusQuarantined || report.PublicEligible || report.TradeEligible {
				t.Fatalf("%s did not fail closed: %+v", test.name, report)
			}
			if len(report.EvidenceRefs) != len(window.Evidence) {
				t.Fatalf("%s quarantine deleted audit evidence: refs=%d evidence=%d", test.name, len(report.EvidenceRefs), len(window.Evidence))
			}
			if !independentContains(report.Reasons, test.reason) {
				t.Fatalf("%s missing stable reason %q: %v", test.name, test.reason, report.Reasons)
			}
		})
	}
}

func TestIndependentSuccessfulNoDataDoesNotSatisfyCapabilityMinimums(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	window := independentPerfectBinanceWindow(t, policy)
	for index := range window.Evidence {
		window.Evidence[index].NoData = true
	}
	report, err := Evaluate(policy, window)
	if err != nil {
		t.Fatal(err)
	}
	if report.AttemptCount != 7 || report.SuccessCount != 7 || report.Status != StatusInsufficient || report.TechnicalScoreBPS != nil || report.Grade != nil {
		t.Fatalf("healthy-empty was presented as usable quality: %+v", report)
	}
	for _, capability := range report.Capabilities {
		if capability.ValidSampleCount != 0 || capability.Status != StatusInsufficient || !independentContains(capability.Reasons, "no_data") {
			t.Fatalf("no-data capability escaped insufficiency: %+v", capability)
		}
	}
}

func TestIndependentGateRequiresThreeHealthyWindowsAndFaultResetsRecovery(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	gate, err := NewGate(policy)
	if err != nil {
		t.Fatal(err)
	}
	state := gate.Advance(Report{Source: policy.Source, Status: StatusQuarantined, Reasons: []string{"hard_fault:future"}})
	if state.Status != StatusQuarantined || state.HealthyWindowStreak != 0 {
		t.Fatalf("initial fault state=%+v", state)
	}
	state = gate.Advance(Report{Source: policy.Source, Status: StatusHealthy})
	if state.Status != StatusRecovering || state.HealthyWindowStreak != 1 {
		t.Fatalf("first healthy window=%+v", state)
	}
	state = gate.Advance(Report{Source: policy.Source, Status: StatusDegraded, Reasons: []string{"rate_limit"}})
	if state.Status != StatusQuarantined || state.HealthyWindowStreak != 0 {
		t.Fatalf("mid-recovery fault failed to reset quarantine: %+v", state)
	}
	for want := uint32(1); want <= policy.RecoveryHealthyWindows; want++ {
		state = gate.Advance(Report{Source: policy.Source, Status: StatusHealthy})
		wantStatus := StatusRecovering
		if want == policy.RecoveryHealthyWindows {
			wantStatus = StatusHealthy
		}
		if state.Status != wantStatus || state.HealthyWindowStreak != want {
			t.Fatalf("healthy window %d state=%+v, want %s streak=%d", want, state, wantStatus, want)
		}
	}
	if state.RecoveryRequired != 3 {
		t.Fatalf("recovery requirement drifted: %+v", state)
	}
}

func TestIndependentMonitorRecoveryRequiresNewEvidenceNotAReplayedWindow(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicies()[SourceBinanceSpot]
	initialEnd := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	clock := &independentMutableClock{now: initialEnd}
	monitor, err := NewMonitor(DefaultPolicies(), clock)
	if err != nil {
		t.Fatal(err)
	}
	faulted := independentPerfectWindowAt(t, policy, initialEnd).Evidence
	faulted[0].HardFaults = []HardFault{HardFaultFuture}
	faulted[0].Metrics[MetricFuture] = Counters{Numerator: 1, Denominator: 1}
	for _, evidence := range faulted {
		if err := monitor.Record(evidence); err != nil {
			t.Fatal(err)
		}
	}
	set, err := monitor.Advance(initialEnd)
	if err != nil {
		t.Fatal(err)
	}
	if report := independentSourceReport(t, set, policy.Source); report.Gate.Status != StatusQuarantined || report.Gate.HealthyWindowStreak != 0 {
		t.Fatalf("fault did not establish quarantine: %+v", report.Gate)
	}

	firstEnd := initialEnd.Add(policy.WindowDuration + time.Second)
	clock.now = firstEnd
	firstBatch := independentPerfectWindowAt(t, policy, firstEnd).Evidence
	for index := range firstBatch {
		firstBatch[index].ID = "recovery-one-" + firstBatch[index].ID
		firstBatch[index].Ref.SourceID = "binance-public:recovery-one:" + firstBatch[index].ID
		if err := monitor.Record(firstBatch[index]); err != nil {
			t.Fatal(err)
		}
	}
	set, err = monitor.Advance(firstEnd)
	if err != nil {
		t.Fatal(err)
	}
	first := independentSourceReport(t, set, policy.Source)
	if first.Gate.Status != StatusRecovering || first.Gate.HealthyWindowStreak != 1 {
		t.Fatalf("first new recovery batch=%+v", first.Gate)
	}

	replayEnd := firstEnd.Add(time.Second)
	clock.now = replayEnd
	set, err = monitor.Advance(replayEnd)
	if err != nil {
		t.Fatal(err)
	}
	replayed := independentSourceReport(t, set, policy.Source)
	if replayed.Gate.Status != StatusRecovering || replayed.Gate.HealthyWindowStreak != 1 || !independentContains(replayed.Reasons, "no_new_evidence") {
		t.Fatalf("old evidence in a later window advanced recovery: report=%+v", replayed)
	}

	for sequence := 2; sequence <= 3; sequence++ {
		end := firstEnd.Add(time.Duration(sequence) * time.Second)
		clock.now = end
		newEvidence := firstBatch[0]
		newEvidence.ID = fmt.Sprintf("recovery-new-%d", sequence)
		newEvidence.At = end.Add(-100 * time.Millisecond)
		newEvidence.Ref.SourceID = fmt.Sprintf("binance-public:recovery-new:%d", sequence)
		if err := monitor.Record(newEvidence); err != nil {
			t.Fatal(err)
		}
		set, err = monitor.Advance(end)
		if err != nil {
			t.Fatal(err)
		}
		report := independentSourceReport(t, set, policy.Source)
		wantStatus := StatusRecovering
		if sequence == 3 {
			wantStatus = StatusHealthy
		}
		if report.Gate.Status != wantStatus || report.Gate.HealthyWindowStreak != uint32(sequence) {
			t.Fatalf("new evidence %d gate=%+v, want %s/%d", sequence, report.Gate, wantStatus, sequence)
		}
	}
}

func TestIndependentMonitorDeduplicatesImmutableFactsAndRejectsConflicts(t *testing.T) {
	t.Parallel()
	policies := DefaultPolicies()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	monitor, err := NewMonitor(policies, independentClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	evidence := independentPerfectWindow(t, policies[SourceXiuqiuResearch]).Evidence[0]
	if err := monitor.Record(evidence); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Record(evidence); err != nil {
		t.Fatalf("identical immutable replay must be idempotent: %v", err)
	}
	conflict := evidence
	conflict.Latency++
	if err := monitor.Record(conflict); err == nil {
		t.Fatal("same evidence ID with different fact must fail closed")
	}
	reports, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	var research Report
	for _, report := range reports.Reports {
		if report.Source == SourceXiuqiuResearch {
			research = report
		}
	}
	if research.Window.SampleCount != 1 || len(research.EvidenceRefs) != 1 || research.EvidenceRefs[0] != evidence.Ref {
		t.Fatalf("monitor duplicated or deleted immutable evidence: %+v", research)
	}
}

func independentPerfectBinanceWindow(t *testing.T, policy Policy) EvidenceWindow {
	t.Helper()
	return independentPerfectWindow(t, policy)
}

func independentPerfectWindow(t *testing.T, policy Policy) EvidenceWindow {
	t.Helper()
	return independentPerfectWindowAt(t, policy, time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC))
}

func independentPerfectWindowAt(t *testing.T, policy Policy, end time.Time) EvidenceWindow {
	t.Helper()
	evidence := make([]Evidence, 0, 7)
	appendEvidence := func(capability Capability, count int) {
		for index := 0; index < count; index++ {
			item := Evidence{
				ID:     fmt.Sprintf("%s-%d", capability, index),
				Source: policy.Source, Capability: capability,
				At:      end.Add(-time.Duration(len(evidence)+1) * time.Second),
				Outcome: OutcomeSuccess, Latency: 100 * time.Millisecond,
				License: LicenseApproved, Live: true,
				Metrics: map[Metric]Counters{
					MetricFreshness:    {Numerator: 1, Denominator: 1},
					MetricCompleteness: {Numerator: 1, Denominator: 1},
					MetricSchema:       {Numerator: 1, Denominator: 1},
					MetricConsistency:  {Numerator: 1, Denominator: 1},
				},
				Ref: EvidenceRef{SourceID: fmt.Sprintf("%s:%s:%d", policy.Source, capability, index)},
			}
			if policy.Source == SourceXiuqiuResearch {
				item.Metrics[MetricResearchSource] = Counters{Numerator: 1, Denominator: 1}
				if capability == CapabilityResearchEvents {
					item.Metrics[MetricResearchWatch] = Counters{Numerator: 1, Denominator: 1}
					item.Metrics[MetricResearchInvalidation] = Counters{Numerator: 1, Denominator: 1}
				}
			}
			evidence = append(evidence, item)
		}
	}
	for _, capability := range policy.RequiredCapabilities {
		appendEvidence(capability, int(policy.CapabilityRules[capability].MinSamples))
	}
	window, err := NewEvidenceWindow(end.Add(-policy.WindowDuration), end, policy.MinSamples, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return window
}

func independentFailureEvidence(at time.Time, id string, outcome Outcome) Evidence {
	return Evidence{
		ID: id, Source: SourceBinanceSpot, Capability: CapabilitySpotTicker,
		At: at, Outcome: outcome, Latency: 100 * time.Millisecond,
		License: LicenseApproved, Live: true,
		Ref: EvidenceRef{SourceID: "binance-public:ticker:" + id},
	}
}

func independentDimension(t *testing.T, report Report, metric Metric) Dimension {
	t.Helper()
	for _, dimension := range report.Dimensions {
		if dimension.Metric == metric {
			return dimension
		}
	}
	t.Fatalf("missing %s dimension", metric)
	return Dimension{}
}

func independentWeightedScore(t *testing.T, policy Policy, dimensions []Dimension) uint32 {
	t.Helper()
	var numerator uint64
	for _, dimension := range dimensions {
		rule := policy.MetricRules[dimension.Metric]
		if rule.WeightBPS == 0 {
			continue
		}
		if dimension.BPS == nil {
			t.Fatalf("weighted dimension %s has no denominator", dimension.Metric)
		}
		qualityBPS := *dimension.BPS
		if rule.Polarity == PolarityFault {
			qualityBPS = BasisPoints - qualityBPS
		}
		numerator += uint64(qualityBPS) * uint64(rule.WeightBPS)
	}
	return uint32((numerator + uint64(BasisPoints)/2) / uint64(BasisPoints))
}

func independentContains(values []string, want string) bool {
	for _, value := range values {
		if value == want || strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func independentSourceReport(t *testing.T, set ReportSet, source SourceKind) Report {
	t.Helper()
	for _, report := range set.Reports {
		if report.Source == source {
			return report
		}
	}
	t.Fatalf("missing report for %s", source)
	return Report{}
}

type independentClock struct{ now time.Time }

func (clock independentClock) Now() time.Time { return clock.now }

var _ Clock = independentClock{}

type independentMutableClock struct{ now time.Time }

func (clock *independentMutableClock) Now() time.Time { return clock.now }

var _ Clock = (*independentMutableClock)(nil)
