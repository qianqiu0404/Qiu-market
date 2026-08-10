package quality

import (
	"fmt"
	"regexp"
	"time"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/=-]{0,127}$`)
var safeSourceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/=@+,;-]{0,511}$`)
var sha256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)

func DefaultPolicies() Policies {
	return Policies{
		SourceBinanceSpot: {
			Source: SourceBinanceSpot, WindowDuration: 5 * time.Minute, MinSamples: 7,
			RequiredCapabilities: []Capability{CapabilitySpotTicker, CapabilityOHLCV},
			CapabilityRules: map[Capability]CapabilityRule{
				CapabilitySpotTicker: {MinSamples: 5, MaxAge: 5 * time.Second},
				CapabilityOHLCV:      {MinSamples: 2, MaxAge: 65 * time.Second},
			},
			MetricRules: metricRules(2500, 2000, 2000, 1500, 1000, 1000),
			MaxLatency:  2 * time.Second, HealthyScoreBPS: 9000, StaleHardFault: true, RecoveryHealthyWindows: 3,
		},
		SourceCoinGlassDerivative: {
			Source: SourceCoinGlassDerivative, WindowDuration: 5 * time.Hour, MinSamples: 2,
			RequiredCapabilities: []Capability{CapabilityOpenInterest, CapabilityLiquidation},
			CapabilityRules: map[Capability]CapabilityRule{
				CapabilityOpenInterest: {MinSamples: 1, MaxAge: 5 * time.Hour},
				CapabilityLiquidation:  {MinSamples: 1, MaxAge: 5 * time.Hour},
			},
			DeclaredCoverageGaps: []Capability{CapabilityFunding},
			MetricRules:          metricRules(2000, 2000, 2000, 1500, 1500, 1000),
			MaxLatency:           5 * time.Second, HealthyScoreBPS: 9000, StaleHardFault: true, RecoveryHealthyWindows: 3,
		},
		SourceXiuqiuResearch: {
			Source: SourceXiuqiuResearch, WindowDuration: 168 * time.Hour, MinSamples: 2,
			RequiredCapabilities: []Capability{CapabilityResearchSummary, CapabilityResearchEvents},
			CapabilityRules: map[Capability]CapabilityRule{
				CapabilityResearchSummary: {MinSamples: 1, MaxAge: 168 * time.Hour},
				CapabilityResearchEvents:  {MinSamples: 1, MaxAge: 168 * time.Hour},
			},
			MetricRules: researchMetricRules(),
			MaxLatency:  5 * time.Second, HealthyScoreBPS: 9000, StaleHardFault: true, RecoveryHealthyWindows: 3,
		},
	}
}

func metricRules(freshness, availability, completeness, schema, consistency, coverage uint32) map[Metric]MetricRule {
	rules := make(map[Metric]MetricRule, len(allMetrics()))
	for _, metric := range allMetrics() {
		polarity := PolarityInformational
		if isFaultMetric(metric) {
			polarity = PolarityFault
		}
		rules[metric] = MetricRule{Polarity: polarity}
	}
	rules[MetricFreshness] = MetricRule{Polarity: PolarityPositive, WeightBPS: freshness, Required: true, ThresholdBPS: 9500}
	rules[MetricAvailability] = MetricRule{Polarity: PolarityPositive, WeightBPS: availability, Required: true, ThresholdBPS: 9500}
	rules[MetricCompleteness] = MetricRule{Polarity: PolarityPositive, WeightBPS: completeness, Required: true, ThresholdBPS: 9500}
	rules[MetricSchema] = MetricRule{Polarity: PolarityPositive, WeightBPS: schema, Required: true, ThresholdBPS: BasisPoints}
	rules[MetricConsistency] = MetricRule{Polarity: PolarityPositive, WeightBPS: consistency, Required: true, ThresholdBPS: BasisPoints}
	rules[MetricCoverage] = MetricRule{Polarity: PolarityPositive, WeightBPS: coverage, Required: true, ThresholdBPS: BasisPoints}
	return rules
}

func researchMetricRules() map[Metric]MetricRule {
	rules := metricRules(1500, 1500, 2000, 1500, 1000, 2500)
	for _, metric := range []Metric{MetricResearchSource, MetricResearchWatch, MetricResearchInvalidation} {
		rules[metric] = MetricRule{Polarity: PolarityPositive, Required: true, ThresholdBPS: BasisPoints}
	}
	return rules
}

func (p Policy) Validate() error { return ValidatePolicy(p) }

func ValidatePolicy(p Policy) error {
	if !validSource(p.Source) || p.WindowDuration <= 0 || p.WindowDuration > 30*24*time.Hour || p.MinSamples == 0 || p.MinSamples > 1_000_000 {
		return fmt.Errorf("%w: source/window/min_samples", ErrInvalidPolicy)
	}
	if p.MaxLatency <= 0 || p.MaxLatency > p.WindowDuration || p.HealthyScoreBPS > BasisPoints || p.RecoveryHealthyWindows == 0 || p.RecoveryHealthyWindows > 100 {
		return fmt.Errorf("%w: latency/score/recovery", ErrInvalidPolicy)
	}
	if len(p.RequiredCapabilities) == 0 || len(p.MetricRules) != len(allMetrics()) {
		return fmt.Errorf("%w: incomplete capabilities or metric dictionary", ErrInvalidPolicy)
	}
	required := make(map[Capability]struct{}, len(p.RequiredCapabilities))
	var capabilityMinimum uint64
	for _, capability := range p.RequiredCapabilities {
		rule, ok := p.CapabilityRules[capability]
		if !validCapabilityForSource(p.Source, capability) || !ok || rule.MinSamples == 0 || rule.MaxAge <= 0 || rule.MaxAge > p.WindowDuration {
			return fmt.Errorf("%w: capability rule %q", ErrInvalidPolicy, capability)
		}
		if _, duplicate := required[capability]; duplicate {
			return fmt.Errorf("%w: duplicate capability %q", ErrInvalidPolicy, capability)
		}
		required[capability] = struct{}{}
		capabilityMinimum += rule.MinSamples
	}
	if p.MinSamples != capabilityMinimum {
		return fmt.Errorf("%w: source min_samples=%d capability total=%d", ErrInvalidPolicy, p.MinSamples, capabilityMinimum)
	}
	if len(p.CapabilityRules) != len(required) {
		return fmt.Errorf("%w: unexpected capability rule", ErrInvalidPolicy)
	}
	seenGaps := map[Capability]struct{}{}
	for _, gap := range p.DeclaredCoverageGaps {
		if !validCapabilityForSource(p.Source, gap) {
			return fmt.Errorf("%w: coverage gap %q", ErrInvalidPolicy, gap)
		}
		if _, overlap := required[gap]; overlap {
			return fmt.Errorf("%w: required capability is also a gap", ErrInvalidPolicy)
		}
		if _, duplicate := seenGaps[gap]; duplicate {
			return fmt.Errorf("%w: duplicate coverage gap", ErrInvalidPolicy)
		}
		seenGaps[gap] = struct{}{}
	}
	var weight uint64
	for _, metric := range allMetrics() {
		rule, ok := p.MetricRules[metric]
		if !ok || !validPolarity(rule.Polarity) || rule.WeightBPS > BasisPoints || rule.ThresholdBPS > BasisPoints {
			return fmt.Errorf("%w: metric rule %q", ErrInvalidPolicy, metric)
		}
		if rule.Polarity == PolarityInformational && rule.WeightBPS != 0 {
			return fmt.Errorf("%w: informational metric %q has weight", ErrInvalidPolicy, metric)
		}
		weight += uint64(rule.WeightBPS)
	}
	if weight != uint64(BasisPoints) {
		return fmt.Errorf("%w: metric weights=%d", ErrInvalidPolicy, weight)
	}
	return nil
}

func ValidateEvidence(e Evidence) error {
	if !safeID.MatchString(e.ID) || !validSource(e.Source) || !validCapabilityForSource(e.Source, e.Capability) || !validOutcome(e.Outcome) || !validLicense(e.License) {
		return fmt.Errorf("%w: identity or enum", ErrInvalidEvidence)
	}
	if e.At.IsZero() || e.At.Location() != time.UTC || e.Latency < 0 || e.Latency > 10*time.Minute {
		return fmt.Errorf("%w: time or latency", ErrInvalidEvidence)
	}
	if !safeSourceID.MatchString(e.Ref.SourceID) || (e.Ref.ContentHash != "" && !sha256Hex.MatchString(e.Ref.ContentHash)) {
		return fmt.Errorf("%w: evidence reference", ErrInvalidEvidence)
	}
	if e.Priorities.P0 > 50 || e.Priorities.P1 > 50 || e.Priorities.P2 > 50 {
		return fmt.Errorf("%w: priority counts", ErrInvalidEvidence)
	}
	priorityTotal := e.Priorities.P0 + e.Priorities.P1 + e.Priorities.P2
	if priorityTotal > 50 {
		return fmt.Errorf("%w: priority counts", ErrInvalidEvidence)
	}
	if e.Source != SourceXiuqiuResearch && (e.Priority != "" || priorityTotal != 0) {
		return fmt.Errorf("%w: priority on non-research evidence", ErrInvalidEvidence)
	}
	if e.Priority != "" && e.Priority != PriorityP0 && e.Priority != PriorityP1 && e.Priority != PriorityP2 {
		return fmt.Errorf("%w: priority", ErrInvalidEvidence)
	}
	if e.Priority != "" && priorityTotal != 0 {
		return fmt.Errorf("%w: priority and priority counts are mutually exclusive", ErrInvalidEvidence)
	}
	if e.NoData && e.Outcome != OutcomeSuccess {
		return fmt.Errorf("%w: no_data requires successful fetch", ErrInvalidEvidence)
	}
	for metric, counter := range e.Metrics {
		if !validMetric(metric) || counter.Numerator > counter.Denominator {
			return fmt.Errorf("%w: counter %q", ErrInvalidEvidence, metric)
		}
	}
	seen := map[HardFault]struct{}{}
	for _, fault := range e.HardFaults {
		if !validHardFault(fault) {
			return fmt.Errorf("%w: hard fault %q", ErrInvalidEvidence, fault)
		}
		if _, duplicate := seen[fault]; duplicate {
			return fmt.Errorf("%w: duplicate hard fault %q", ErrInvalidEvidence, fault)
		}
		seen[fault] = struct{}{}
	}
	return nil
}

func ValidatePolicies(policies Policies) error {
	if len(policies) != 3 {
		return fmt.Errorf("%w: exactly three independent policies required", ErrInvalidPolicy)
	}
	for _, source := range stableSources() {
		policy, ok := policies[source]
		if !ok || policy.Source != source {
			return fmt.Errorf("%w: missing policy %q", ErrInvalidPolicy, source)
		}
		if err := policy.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func stableSources() []SourceKind {
	return []SourceKind{SourceBinanceSpot, SourceCoinGlassDerivative, SourceXiuqiuResearch}
}
func validSource(v SourceKind) bool {
	return v == SourceBinanceSpot || v == SourceCoinGlassDerivative || v == SourceXiuqiuResearch
}
func validPolarity(v Polarity) bool {
	return v == PolarityPositive || v == PolarityFault || v == PolarityInformational
}
func validLicense(v LicenseStatus) bool {
	return v == LicenseApproved || v == LicenseUnknown || v == LicenseRestricted || v == LicenseProhibited
}
func validOutcome(v Outcome) bool {
	switch v {
	case OutcomeSuccess, OutcomeRateLimit, OutcomeUpstream5xx, OutcomeTimeout, OutcomeBadPayload, OutcomeUnsupported, OutcomeAuth, OutcomePermission, OutcomeNetwork, OutcomeUnconfigured, OutcomeStale:
		return true
	}
	return false
}
func validCapabilityForSource(source SourceKind, capability Capability) bool {
	switch source {
	case SourceBinanceSpot:
		return capability == CapabilitySpotTicker || capability == CapabilityOHLCV
	case SourceCoinGlassDerivative:
		return capability == CapabilityOpenInterest || capability == CapabilityLiquidation || capability == CapabilityFunding
	case SourceXiuqiuResearch:
		return capability == CapabilityResearchSummary || capability == CapabilityResearchEvents
	}
	return false
}
func allMetrics() []Metric {
	return []Metric{MetricFreshness, MetricLatency, MetricAvailability, MetricCompleteness, MetricSchema, MetricConsistency, MetricDuplicate, MetricConflict, MetricOutOfOrder, MetricFuture, MetricStale, MetricUnit, MetricPrecision, MetricIdentity, MetricCoverage, MetricRateLimit, MetricUpstream5xx, MetricTimeout, MetricCacheHit, MetricStaleServe, MetricResearchSource, MetricResearchWatch, MetricResearchInvalidation, MetricResearchLegacy, MetricResearchPriority, MetricContentHashConflict}
}
func validMetric(v Metric) bool {
	for _, metric := range allMetrics() {
		if metric == v {
			return true
		}
	}
	return false
}
func isFaultMetric(v Metric) bool {
	switch v {
	case MetricDuplicate, MetricConflict, MetricOutOfOrder, MetricFuture, MetricStale, MetricUnit, MetricPrecision, MetricIdentity, MetricRateLimit, MetricUpstream5xx, MetricTimeout, MetricStaleServe, MetricResearchLegacy, MetricContentHashConflict:
		return true
	}
	return false
}
func validHardFault(v HardFault) bool {
	switch v {
	case HardFaultFuture, HardFaultSchema, HardFaultIdentity, HardFaultUnit, HardFaultPrecision, HardFaultConflict, HardFaultStaleServe, HardFaultContentHashConflict, HardFaultStale:
		return true
	}
	return false
}
