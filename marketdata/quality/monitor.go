package quality

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Clock interface{ Now() time.Time }
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Monitor struct {
	mu             sync.RWMutex
	policies       Policies
	clock          Clock
	evidence       map[SourceKind]map[string]Evidence
	gates          map[SourceKind]*Gate
	evaluatedIDs   map[SourceKind]map[string]struct{}
	lastAdvanceEnd time.Time
	snapshot       ReportSet
}

const maxEvidencePerSource = 4096

func NewMonitor(policies Policies, clock Clock) (*Monitor, error) {
	if err := ValidatePolicies(policies); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, fmt.Errorf("quality: nil clock")
	}
	cloned := make(Policies, len(policies))
	for source, policy := range policies {
		cloned[source] = clonePolicy(policy)
	}
	gates := make(map[SourceKind]*Gate, len(cloned))
	for source, policy := range cloned {
		gate, err := NewGate(policy)
		if err != nil {
			return nil, err
		}
		gates[source] = gate
	}
	return &Monitor{policies: cloned, clock: clock, evidence: make(map[SourceKind]map[string]Evidence), gates: gates, evaluatedIDs: make(map[SourceKind]map[string]struct{})}, nil
}

// Record is idempotent for the same immutable evidence ID. Reusing an ID for a
// different fact fails closed instead of inflating a denominator.
func (m *Monitor) Record(evidence Evidence) error {
	if err := ValidateEvidence(evidence); err != nil {
		return err
	}
	if _, ok := m.policies[evidence.Source]; !ok {
		return fmt.Errorf("%w: source has no policy", ErrInvalidEvidence)
	}
	cloned := cloneEvidence(evidence)
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := m.evidence[evidence.Source]
	if byID == nil {
		byID = make(map[string]Evidence)
		m.evidence[evidence.Source] = byID
	}
	if existing, ok := byID[evidence.ID]; ok {
		if !evidenceEqual(existing, cloned) {
			return fmt.Errorf("%w: evidence ID conflict", ErrInvalidEvidence)
		}
		return nil
	}
	cutoff := m.clock.Now().UTC().Add(-m.policies[evidence.Source].WindowDuration)
	for id, item := range byID {
		if item.At.Before(cutoff) {
			delete(byID, id)
			delete(m.evaluatedIDs[evidence.Source], id)
		}
	}
	if len(byID) >= maxEvidencePerSource {
		return fmt.Errorf("%w: source=%s capacity=%d", ErrEvidenceLimit, evidence.Source, maxEvidencePerSource)
	}
	byID[evidence.ID] = cloned
	return nil
}

func (m *Monitor) Reports() (ReportSet, error) {
	now := m.clock.Now()
	if now.Location() != time.UTC {
		return ReportSet{}, fmt.Errorf("quality: clock must return UTC")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.snapshot.Reports) > 0 {
		return cloneReportSet(m.snapshot), nil
	}
	return m.evaluateLocked(now, false)
}

// Advance closes one evidence window and advances quarantine recovery exactly
// once. Replaying the same End is idempotent; older windows fail closed.
func (m *Monitor) Advance(end time.Time) (ReportSet, error) {
	if end.Location() != time.UTC {
		return ReportSet{}, fmt.Errorf("%w: end must be UTC", ErrInvalidWindow)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastAdvanceEnd.IsZero() {
		if end.Equal(m.lastAdvanceEnd) {
			return cloneReportSet(m.snapshot), nil
		}
		if end.Before(m.lastAdvanceEnd) {
			return ReportSet{}, fmt.Errorf("%w: non-monotonic window end", ErrInvalidWindow)
		}
	}
	set, err := m.evaluateLocked(end, true)
	if err != nil {
		return ReportSet{}, err
	}
	m.lastAdvanceEnd = end
	m.snapshot = cloneReportSet(set)
	return cloneReportSet(set), nil
}

func (m *Monitor) evaluateLocked(end time.Time, advance bool) (ReportSet, error) {
	set := ReportSet{Reports: make([]Report, 0, len(stableSources()))}
	for _, source := range stableSources() {
		policy := m.policies[source]
		start := end.Add(-policy.WindowDuration)
		items := make([]Evidence, 0, len(m.evidence[source]))
		for _, evidence := range m.evidence[source] {
			if !evidence.At.Before(start) && evidence.At.Before(end) {
				items = append(items, cloneEvidence(evidence))
			}
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].At.Equal(items[j].At) {
				return items[i].ID < items[j].ID
			}
			return items[i].At.Before(items[j].At)
		})
		window, err := NewEvidenceWindow(start, end, policy.MinSamples, items)
		if err != nil {
			return ReportSet{}, err
		}
		report, err := Evaluate(policy, window)
		if err != nil {
			return ReportSet{}, err
		}
		var gateState GateState
		if advance {
			hasNew := false
			seen := m.evaluatedIDs[source]
			if seen == nil {
				seen = make(map[string]struct{})
				m.evaluatedIDs[source] = seen
			}
			for _, item := range items {
				if _, ok := seen[item.ID]; !ok && item.Outcome == OutcomeSuccess && !item.CacheHit && !item.NoData && !evidenceHasHardFault(item, policy) {
					hasNew = true
				}
			}
			if hasNew || report.Status != StatusHealthy {
				gateState = m.gates[source].Advance(report)
			} else {
				gateState = m.gates[source].State()
				report.Reasons = uniqueSortedStrings(append(report.Reasons, "no_new_evidence"))
			}
			for _, item := range items {
				seen[item.ID] = struct{}{}
			}
		} else {
			gateState = GateState{Source: source, Status: report.Status, RecoveryRequired: policy.RecoveryHealthyWindows}
		}
		report.Gate = gateState
		report.Status = gateState.Status
		if report.Status != StatusHealthy {
			report.PublicEligible, report.ResearchContextEligible = false, false
		}
		set.Reports = append(set.Reports, report)
	}
	return set, nil
}

func cloneReportSet(in ReportSet) ReportSet {
	out := ReportSet{Reports: make([]Report, len(in.Reports))}
	for i := range in.Reports {
		report := in.Reports[i]
		report.Window = cloneWindow(report.Window)
		report.Dimensions = append([]Dimension(nil), report.Dimensions...)
		for j := range report.Dimensions {
			if report.Dimensions[j].BPS != nil {
				value := *report.Dimensions[j].BPS
				report.Dimensions[j].BPS = &value
			}
		}
		report.Capabilities = append([]CapabilityReport(nil), report.Capabilities...)
		for j := range report.Capabilities {
			report.Capabilities[j].Reasons = append([]string(nil), report.Capabilities[j].Reasons...)
			report.Capabilities[j].LastAttemptAt = cloneTime(report.Capabilities[j].LastAttemptAt)
			report.Capabilities[j].LastSuccessAt = cloneTime(report.Capabilities[j].LastSuccessAt)
			report.Capabilities[j].Age = cloneDuration(report.Capabilities[j].Age)
		}
		report.TechnicalScoreBPS = cloneUint32(report.TechnicalScoreBPS)
		if report.Grade != nil {
			value := *report.Grade
			report.Grade = &value
		}
		report.CoverageGaps = append([]Capability(nil), report.CoverageGaps...)
		report.Reasons = append([]string(nil), report.Reasons...)
		report.EvidenceRefs = append([]EvidenceRef(nil), report.EvidenceRefs...)
		report.LastAttemptAt = cloneTime(report.LastAttemptAt)
		report.LastSuccessAt = cloneTime(report.LastSuccessAt)
		report.Age = cloneDuration(report.Age)
		report.ErrorCounts = make(map[Outcome]uint64, len(in.Reports[i].ErrorCounts))
		for key, value := range in.Reports[i].ErrorCounts {
			report.ErrorCounts[key] = value
		}
		report.Gate = cloneGateState(report.Gate)
		out.Reports[i] = report
	}
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePolicy(in Policy) Policy {
	out := in
	out.RequiredCapabilities = append([]Capability(nil), in.RequiredCapabilities...)
	out.DeclaredCoverageGaps = append([]Capability(nil), in.DeclaredCoverageGaps...)
	out.CapabilityRules = make(map[Capability]CapabilityRule, len(in.CapabilityRules))
	for key, value := range in.CapabilityRules {
		out.CapabilityRules[key] = value
	}
	out.MetricRules = make(map[Metric]MetricRule, len(in.MetricRules))
	for key, value := range in.MetricRules {
		out.MetricRules[key] = value
	}
	return out
}

func evidenceEqual(a, b Evidence) bool {
	if a.ID != b.ID || a.Source != b.Source || a.Capability != b.Capability || !a.At.Equal(b.At) || a.Outcome != b.Outcome || a.Latency != b.Latency || a.License != b.License || a.CacheHit != b.CacheHit || a.StaleServe != b.StaleServe || a.NoData != b.NoData || a.Live != b.Live || a.Priority != b.Priority || a.Priorities != b.Priorities || a.Ref != b.Ref || len(a.Metrics) != len(b.Metrics) || len(a.HardFaults) != len(b.HardFaults) {
		return false
	}
	for metric, counters := range a.Metrics {
		if b.Metrics[metric] != counters {
			return false
		}
	}
	for i := range a.HardFaults {
		if a.HardFaults[i] != b.HardFaults[i] {
			return false
		}
	}
	return true
}
