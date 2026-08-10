package quality

import (
	"fmt"
	"math"
	"sort"
	"time"
)

func Evaluate(policy Policy, window EvidenceWindow) (Report, error) {
	if err := policy.Validate(); err != nil {
		return Report{}, err
	}
	if err := window.Validate(); err != nil {
		return Report{}, err
	}
	report := Report{
		Source: policy.Source, Window: cloneWindow(window), License: LicenseUnknown,
		CoverageGaps: append([]Capability(nil), policy.DeclaredCoverageGaps...),
		ErrorCounts:  make(map[Outcome]uint64), DiagnosticEligible: len(window.Evidence) > 0,
	}
	aggregates := make(map[Metric]Counters, len(allMetrics()))
	capabilities := make(map[Capability]*CapabilityReport, len(policy.RequiredCapabilities))
	capabilityHard := make(map[Capability]bool, len(policy.RequiredCapabilities))
	for _, capability := range policy.RequiredCapabilities {
		rule := policy.CapabilityRules[capability]
		capabilities[capability] = &CapabilityReport{Capability: capability, MinSamples: rule.MinSamples, MaxAge: rule.MaxAge, Status: StatusInsufficient}
	}
	licenseSet := false
	allLive := true
	hardReasons := map[string]struct{}{}
	for i := range window.Evidence {
		evidence := window.Evidence[i]
		if err := ValidateEvidence(evidence); err != nil {
			return Report{}, fmt.Errorf("evidence[%d]: %w", i, err)
		}
		if evidence.Source != policy.Source {
			return Report{}, fmt.Errorf("%w: evidence source %q does not match policy %q", ErrInvalidEvidence, evidence.Source, policy.Source)
		}
		report.EvidenceRefs = append(report.EvidenceRefs, evidence.Ref)
		if !licenseSet || licenseRank(evidence.License) > licenseRank(report.License) {
			report.License, licenseSet = evidence.License, true
		}
		if !evidence.Live {
			allLive = false
		}
		capabilityReport := capabilities[evidence.Capability]
		if evidence.Outcome == OutcomeUnsupported {
			if !containsCapability(policy.DeclaredCoverageGaps, evidence.Capability) {
				report.Reasons = append(report.Reasons, "unexpected_unsupported:"+string(evidence.Capability))
			}
			continue
		}
		if evidence.CacheHit {
			report.CacheHitCount++
			if !addCounters(aggregates, MetricCacheHit, Counters{Numerator: 1, Denominator: 1}) {
				return Report{}, fmt.Errorf("%w: metric counter overflow", ErrInvalidEvidence)
			}
			if evidence.StaleServe {
				report.StaleServeCount++
				if !addCounters(aggregates, MetricStaleServe, Counters{Numerator: 1, Denominator: 1}) {
					return Report{}, fmt.Errorf("%w: metric counter overflow", ErrInvalidEvidence)
				}
				hardReasons[string(HardFaultStaleServe)] = struct{}{}
			}
			for _, fault := range evidence.HardFaults {
				hardReasons[string(fault)] = struct{}{}
			}
			continue
		}
		report.AttemptCount++
		if capabilityReport != nil {
			capabilityReport.SampleCount++
			setLatest(&capabilityReport.LastAttemptAt, evidence.At)
		}
		setLatest(&report.LastAttemptAt, evidence.At)
		if evidence.Outcome == OutcomeSuccess {
			report.SuccessCount++
			setLatest(&report.LastSuccessAt, evidence.At)
			if capabilityReport != nil {
				capabilityReport.SuccessCount++
				setLatest(&capabilityReport.LastSuccessAt, evidence.At)
				if !evidence.NoData && !evidenceHasHardFault(evidence, policy) {
					capabilityReport.ValidSampleCount++
				}
			}
		} else {
			report.ErrorCounts[evidence.Outcome]++
		}
		if evidence.StaleServe {
			report.StaleServeCount++
			hardReasons[string(HardFaultStaleServe)] = struct{}{}
		}
		if !addDerived(aggregates, evidence, policy) {
			return Report{}, fmt.Errorf("%w: derived counter overflow", ErrInvalidEvidence)
		}
		for metric, counters := range evidence.Metrics {
			if !addCounters(aggregates, metric, counters) {
				return Report{}, fmt.Errorf("%w: metric %q counter overflow", ErrInvalidEvidence, metric)
			}
		}
		priorityCounts := evidence.Priorities
		switch evidence.Priority {
		case PriorityP0:
			priorityCounts.P0 = 1
		case PriorityP1:
			priorityCounts.P1 = 1
		case PriorityP2:
			priorityCounts.P2 = 1
		}
		if !addPriorityCounts(&report.PriorityCounts, priorityCounts) {
			return Report{}, fmt.Errorf("%w: priority counter overflow", ErrInvalidEvidence)
		}
		for _, fault := range evidence.HardFaults {
			hardReasons[string(fault)] = struct{}{}
		}
		if evidenceHasHardFault(evidence, policy) {
			capabilityHard[evidence.Capability] = true
		}
	}
	if !licenseSet {
		report.License = LicenseUnknown
		allLive = false
	}
	coverage := Counters{Denominator: uint64(len(policy.RequiredCapabilities))}
	capabilityInsufficient := false
	for _, capability := range policy.RequiredCapabilities {
		capReport := capabilities[capability]
		if capReport.LastSuccessAt != nil {
			age := window.End.Sub(*capReport.LastSuccessAt)
			capReport.Age = &age
		}
		if capReport.SuccessCount >= capReport.MinSamples {
			coverage.Numerator++
		}
		switch {
		case capabilityHard[capability]:
			capReport.Status = StatusQuarantined
			capReport.Reasons = append(capReport.Reasons, "hard_fault")
		case capReport.ValidSampleCount < capReport.MinSamples:
			capReport.Status = StatusInsufficient
			capReport.Reasons = append(capReport.Reasons, "min_samples")
			if capReport.SuccessCount > 0 && capReport.ValidSampleCount == 0 {
				capReport.Reasons = append(capReport.Reasons, "no_data")
			}
			capabilityInsufficient = true
		case capReport.Age == nil:
			capReport.Status = StatusInsufficient
			capReport.Reasons = append(capReport.Reasons, "missing_success")
			capabilityInsufficient = true
		case *capReport.Age < 0:
			capReport.Status = StatusQuarantined
			capReport.Reasons = append(capReport.Reasons, "future")
			hardReasons[string(HardFaultFuture)] = struct{}{}
		case *capReport.Age > capReport.MaxAge:
			capReport.Status = StatusQuarantined
			capReport.Reasons = append(capReport.Reasons, "stale")
			if policy.StaleHardFault {
				hardReasons[string(HardFaultStale)] = struct{}{}
			}
		default:
			if capReport.SampleCount > capReport.SuccessCount {
				capReport.Status = StatusDegraded
				capReport.Reasons = append(capReport.Reasons, "attempt_failures")
			} else {
				capReport.Status = StatusHealthy
			}
		}
		report.Capabilities = append(report.Capabilities, *capReport)
	}
	aggregates[MetricCoverage] = coverage
	if policy.Source == SourceXiuqiuResearch {
		for _, contextMetric := range []Metric{MetricResearchSource, MetricResearchWatch, MetricResearchInvalidation} {
			if !addCounters(aggregates, MetricCoverage, aggregates[contextMetric]) {
				return Report{}, fmt.Errorf("%w: research coverage counter overflow", ErrInvalidEvidence)
			}
		}
	}
	if report.LastSuccessAt != nil {
		age := window.End.Sub(*report.LastSuccessAt)
		report.Age = &age
	}
	for _, metric := range allMetrics() {
		rule := policy.MetricRules[metric]
		counter := aggregates[metric]
		report.Dimensions = append(report.Dimensions, Dimension{Metric: metric, Polarity: rule.Polarity, Numerator: counter.Numerator, Denominator: counter.Denominator, BPS: counter.BPS()})
		if reason, hard := metricHardFaultReason(metric, counter, policy); hard {
			hardReasons[reason] = struct{}{}
		}
	}
	insufficient := window.SampleCount < window.MinSamples || report.AttemptCount < policy.MinSamples || capabilityInsufficient
	score, thresholdsHealthy := technicalScore(policy, report.Dimensions, &insufficient)
	report.TechnicalScoreBPS = score
	if score != nil {
		grade := gradeFor(*score)
		report.Grade = &grade
	}
	if window.SampleCount == 0 {
		report.Reasons = append(report.Reasons, "empty_window")
	}
	if insufficient {
		report.Reasons = append(report.Reasons, "insufficient_evidence")
	}
	for reason := range hardReasons {
		report.Reasons = append(report.Reasons, "hard_fault:"+reason)
	}
	if report.License != LicenseApproved {
		report.Reasons = append(report.Reasons, "license_"+string(report.License))
	}
	if !allLive {
		report.Reasons = append(report.Reasons, "not_live")
	}
	report.Reasons = uniqueSortedStrings(report.Reasons)
	sort.Slice(report.EvidenceRefs, func(i, j int) bool {
		if report.EvidenceRefs[i].SourceID == report.EvidenceRefs[j].SourceID {
			return report.EvidenceRefs[i].ContentHash < report.EvidenceRefs[j].ContentHash
		}
		return report.EvidenceRefs[i].SourceID < report.EvidenceRefs[j].SourceID
	})
	report.EvidenceRefs = uniqueRefs(report.EvidenceRefs)
	switch {
	case len(hardReasons) > 0:
		report.Status = StatusQuarantined
	case report.License != LicenseApproved && report.AttemptCount > 0:
		report.Status = StatusQuarantined
	case insufficient || score == nil:
		report.Status = StatusInsufficient
	case *score >= policy.HealthyScoreBPS && thresholdsHealthy:
		report.Status = StatusHealthy
	default:
		report.Status = StatusDegraded
	}
	report.PublicEligible = report.Status == StatusHealthy && report.License == LicenseApproved && allLive
	report.ResearchContextEligible = report.Source == SourceXiuqiuResearch && report.PublicEligible
	return report, nil
}

func addDerived(dst map[Metric]Counters, evidence Evidence, policy Policy) bool {
	if evidence.Outcome != OutcomeUnsupported {
		if !addIfAbsent(dst, evidence.Metrics, MetricAvailability, boolCounter(evidence.Outcome == OutcomeSuccess)) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricRateLimit, boolCounter(evidence.Outcome == OutcomeRateLimit)) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricUpstream5xx, boolCounter(evidence.Outcome == OutcomeUpstream5xx)) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricTimeout, boolCounter(evidence.Outcome == OutcomeTimeout)) {
			return false
		}
	}
	if evidence.Outcome == OutcomeSuccess {
		if !addIfAbsent(dst, evidence.Metrics, MetricLatency, boolCounter(evidence.Latency <= policy.MaxLatency)) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricCacheHit, boolCounter(evidence.CacheHit)) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricStaleServe, boolCounter(evidence.StaleServe)) {
			return false
		}
	}
	if evidence.Outcome == OutcomeSuccess || evidence.Outcome == OutcomeBadPayload {
		if !addIfAbsent(dst, evidence.Metrics, MetricSchema, boolCounter(evidence.Outcome == OutcomeSuccess)) {
			return false
		}
	}
	if evidence.Outcome == OutcomeSuccess {
		consistent := !evidenceHasConsistencyFault(evidence)
		if !addIfAbsent(dst, evidence.Metrics, MetricConsistency, boolCounter(consistent)) {
			return false
		}
	}
	if evidence.Outcome == OutcomeStale {
		if !addIfAbsent(dst, evidence.Metrics, MetricFreshness, Counters{Denominator: 1}) {
			return false
		}
		if !addIfAbsent(dst, evidence.Metrics, MetricStale, Counters{Numerator: 1, Denominator: 1}) {
			return false
		}
	}
	return true
}

func addIfAbsent(dst map[Metric]Counters, supplied map[Metric]Counters, metric Metric, value Counters) bool {
	if _, ok := supplied[metric]; !ok {
		return addCounters(dst, metric, value)
	}
	return true
}
func boolCounter(ok bool) Counters {
	c := Counters{Denominator: 1}
	if ok {
		c.Numerator = 1
	}
	return c
}
func addCounters(dst map[Metric]Counters, metric Metric, value Counters) bool {
	current := dst[metric]
	if value.Numerator > math.MaxUint64-current.Numerator || value.Denominator > math.MaxUint64-current.Denominator {
		return false
	}
	current.Numerator += value.Numerator
	current.Denominator += value.Denominator
	dst[metric] = current
	return true
}
func evidenceHasConsistencyFault(e Evidence) bool {
	for _, fault := range e.HardFaults {
		if fault == HardFaultFuture || fault == HardFaultConflict || fault == HardFaultContentHashConflict {
			return true
		}
	}
	for _, metric := range []Metric{MetricDuplicate, MetricConflict, MetricOutOfOrder, MetricFuture, MetricContentHashConflict} {
		if e.Metrics[metric].Numerator > 0 {
			return true
		}
	}
	return false
}
func evidenceHasHardFault(e Evidence, policy Policy) bool {
	if e.Outcome == OutcomeBadPayload || e.Outcome == OutcomeStale || e.StaleServe || len(e.HardFaults) > 0 {
		return true
	}
	for metric, counters := range e.Metrics {
		if _, hard := metricHardFaultReason(metric, counters, policy); hard {
			return true
		}
	}
	return false
}
func metricHardFaultReason(metric Metric, counter Counters, policy Policy) (string, bool) {
	switch metric {
	case MetricSchema:
		return string(HardFaultSchema), counter.Denominator > 0 && counter.Numerator < counter.Denominator
	case MetricFreshness:
		return string(HardFaultStale), policy.StaleHardFault && counter.Denominator > 0 && counter.Numerator < counter.Denominator
	case MetricFuture, MetricIdentity, MetricUnit, MetricPrecision, MetricConflict, MetricStaleServe, MetricContentHashConflict:
		return string(metric), counter.Numerator > 0
	case MetricStale:
		return string(HardFaultStale), policy.StaleHardFault && counter.Numerator > 0
	default:
		return "", false
	}
}
func technicalScore(policy Policy, dimensions []Dimension, insufficient *bool) (*uint32, bool) {
	var weighted uint64
	healthy := true
	for _, dimension := range dimensions {
		rule := policy.MetricRules[dimension.Metric]
		if rule.Required && dimension.Denominator == 0 {
			*insufficient = true
		}
		if rule.WeightBPS == 0 || dimension.BPS == nil {
			continue
		}
		quality := *dimension.BPS
		if rule.Polarity == PolarityFault {
			quality = BasisPoints - quality
			if *dimension.BPS > rule.ThresholdBPS {
				healthy = false
			}
		} else if quality < rule.ThresholdBPS {
			healthy = false
		}
		weighted += uint64(quality) * uint64(rule.WeightBPS)
	}
	if *insufficient {
		return nil, false
	}
	score := uint32((weighted + uint64(BasisPoints)/2) / uint64(BasisPoints))
	return &score, healthy
}
func gradeFor(score uint32) Grade {
	switch {
	case score >= 9000:
		return GradeA
	case score >= 8000:
		return GradeB
	case score >= 7000:
		return GradeC
	case score >= 5000:
		return GradeD
	default:
		return GradeF
	}
}
func licenseRank(v LicenseStatus) int {
	switch v {
	case LicenseApproved:
		return 0
	case LicenseUnknown:
		return 1
	case LicenseRestricted:
		return 2
	case LicenseProhibited:
		return 3
	}
	return 4
}
func containsCapability(items []Capability, target Capability) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
func setLatest(target **time.Time, candidate time.Time) {
	if *target == nil || candidate.After(**target) {
		value := candidate
		*target = &value
	}
}
func cloneWindow(window EvidenceWindow) EvidenceWindow {
	cloned, _ := NewEvidenceWindow(window.Start, window.End, window.MinSamples, window.Evidence)
	return cloned
}
func uniqueSortedStrings(items []string) []string {
	sort.Strings(items)
	out := items[:0]
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}
func uniqueRefs(items []EvidenceRef) []EvidenceRef {
	out := items[:0]
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}
func addPriorityCounts(target *PriorityCounts, value PriorityCounts) bool {
	if value.P0 > math.MaxUint64-target.P0 || value.P1 > math.MaxUint64-target.P1 || value.P2 > math.MaxUint64-target.P2 {
		return false
	}
	target.P0 += value.P0
	target.P1 += value.P1
	target.P2 += value.P2
	return true
}
