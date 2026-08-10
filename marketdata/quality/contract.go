// Package quality evaluates provider evidence without importing or mutating any
// trading, persistence, or provider implementation package.
package quality

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"time"
)

const BasisPoints = uint32(10_000)

type SourceKind string

const (
	SourceBinanceSpot         SourceKind = "binance_spot"
	SourceCoinGlassDerivative SourceKind = "coinglass_derivatives"
	SourceXiuqiuResearch      SourceKind = "xiuqiu_research"
)

type Capability string

const (
	CapabilitySpotTicker      Capability = "spot_ticker"
	CapabilityOHLCV           Capability = "ohlcv"
	CapabilityOpenInterest    Capability = "open_interest"
	CapabilityLiquidation     Capability = "liquidation"
	CapabilityFunding         Capability = "funding"
	CapabilityResearchSummary Capability = "research_summary"
	CapabilityResearchEvents  Capability = "research_events"
)

type Outcome string

const (
	OutcomeSuccess      Outcome = "success"
	OutcomeRateLimit    Outcome = "rate_limit"
	OutcomeUpstream5xx  Outcome = "upstream_5xx"
	OutcomeTimeout      Outcome = "timeout"
	OutcomeBadPayload   Outcome = "bad_payload"
	OutcomeUnsupported  Outcome = "unsupported"
	OutcomeAuth         Outcome = "auth"
	OutcomePermission   Outcome = "permission"
	OutcomeNetwork      Outcome = "network"
	OutcomeUnconfigured Outcome = "unconfigured"
	OutcomeStale        Outcome = "stale"
)

type LicenseStatus string

const (
	LicenseApproved   LicenseStatus = "approved"
	LicenseUnknown    LicenseStatus = "unknown"
	LicenseRestricted LicenseStatus = "restricted"
	LicenseProhibited LicenseStatus = "prohibited"
)

type ResearchPriority string

const (
	PriorityP0 ResearchPriority = "p0"
	PriorityP1 ResearchPriority = "p1"
	PriorityP2 ResearchPriority = "p2"
)

type Status string

const (
	StatusInsufficient Status = "insufficient"
	StatusHealthy      Status = "healthy"
	StatusDegraded     Status = "degraded"
	StatusQuarantined  Status = "quarantined"
	StatusRecovering   Status = "recovering"
)

type Grade string

const (
	GradeA Grade = "A"
	GradeB Grade = "B"
	GradeC Grade = "C"
	GradeD Grade = "D"
	GradeF Grade = "F"
)

type Metric string

const (
	MetricFreshness            Metric = "freshness"
	MetricLatency              Metric = "latency"
	MetricAvailability         Metric = "availability"
	MetricCompleteness         Metric = "completeness"
	MetricSchema               Metric = "schema"
	MetricConsistency          Metric = "consistency"
	MetricDuplicate            Metric = "duplicate"
	MetricConflict             Metric = "conflict"
	MetricOutOfOrder           Metric = "out_of_order"
	MetricFuture               Metric = "future"
	MetricStale                Metric = "stale"
	MetricUnit                 Metric = "unit"
	MetricPrecision            Metric = "precision"
	MetricIdentity             Metric = "identity"
	MetricCoverage             Metric = "coverage"
	MetricRateLimit            Metric = "rate_limit"
	MetricUpstream5xx          Metric = "upstream_5xx"
	MetricTimeout              Metric = "timeout"
	MetricCacheHit             Metric = "cache_hit"
	MetricStaleServe           Metric = "stale_serve"
	MetricResearchSource       Metric = "research_source"
	MetricResearchWatch        Metric = "research_watch"
	MetricResearchInvalidation Metric = "research_invalidation"
	MetricResearchLegacy       Metric = "research_legacy"
	MetricResearchPriority     Metric = "research_priority"
	MetricContentHashConflict  Metric = "content_hash_conflict"
)

type Polarity string

const (
	PolarityPositive      Polarity = "positive"
	PolarityFault         Polarity = "fault"
	PolarityInformational Polarity = "informational"
)

type HardFault string

const (
	HardFaultFuture              HardFault = "future"
	HardFaultSchema              HardFault = "schema"
	HardFaultIdentity            HardFault = "identity"
	HardFaultUnit                HardFault = "unit"
	HardFaultPrecision           HardFault = "precision"
	HardFaultConflict            HardFault = "conflict"
	HardFaultStaleServe          HardFault = "stale_serve"
	HardFaultContentHashConflict HardFault = "content_hash_conflict"
	HardFaultStale               HardFault = "stale"
)

// Counters is an exact ratio. BPS is deliberately absent when Denominator is
// zero; an empty denominator must never be presented as perfect quality.
type Counters struct {
	Numerator   uint64 `json:"numerator"`
	Denominator uint64 `json:"denominator"`
}

func (c Counters) BPS() *uint32 {
	if c.Denominator == 0 || c.Numerator > c.Denominator {
		return nil
	}
	whole, remainder := c.Numerator/c.Denominator, c.Numerator%c.Denominator
	hi, lo := bits.Mul64(remainder, uint64(BasisPoints))
	fraction, roundingRemainder := bits.Div64(hi, lo, c.Denominator)
	if roundingRemainder >= c.Denominator/2+c.Denominator%2 {
		fraction++
	}
	v := uint32(whole*uint64(BasisPoints) + fraction)
	return &v
}

type MetricRule struct {
	Polarity     Polarity `json:"polarity"`
	WeightBPS    uint32   `json:"weight_bps"`
	Required     bool     `json:"required"`
	ThresholdBPS uint32   `json:"threshold_bps"`
}

type CapabilityRule struct {
	MinSamples uint64        `json:"min_samples"`
	MaxAge     time.Duration `json:"max_age"`
}

type Policy struct {
	Source                 SourceKind                    `json:"source"`
	WindowDuration         time.Duration                 `json:"window_duration"`
	MinSamples             uint64                        `json:"min_samples"`
	RequiredCapabilities   []Capability                  `json:"required_capabilities"`
	CapabilityRules        map[Capability]CapabilityRule `json:"capability_rules"`
	DeclaredCoverageGaps   []Capability                  `json:"declared_coverage_gaps,omitempty"`
	MetricRules            map[Metric]MetricRule         `json:"metric_rules"`
	MaxLatency             time.Duration                 `json:"max_latency"`
	HealthyScoreBPS        uint32                        `json:"healthy_score_bps"`
	StaleHardFault         bool                          `json:"stale_hard_fault"`
	RecoveryHealthyWindows uint32                        `json:"recovery_healthy_windows"`
}

type Policies map[SourceKind]Policy

// EvidenceRef is an auditable identifier or content hash. It must not contain
// a raw provider payload, credentials, or restricted content.
type EvidenceRef struct {
	SourceID    string `json:"source_id"`
	ContentHash string `json:"content_hash,omitempty"`
}

type Evidence struct {
	ID         string              `json:"id"`
	Source     SourceKind          `json:"source"`
	Capability Capability          `json:"capability"`
	At         time.Time           `json:"at"`
	Outcome    Outcome             `json:"outcome"`
	Latency    time.Duration       `json:"latency"`
	License    LicenseStatus       `json:"license"`
	Metrics    map[Metric]Counters `json:"metrics,omitempty"`
	HardFaults []HardFault         `json:"hard_faults,omitempty"`
	CacheHit   bool                `json:"cache_hit"`
	StaleServe bool                `json:"stale_serve"`
	NoData     bool                `json:"no_data"`
	Live       bool                `json:"live"`
	Priority   ResearchPriority    `json:"research_priority,omitempty"`
	Priorities PriorityCounts      `json:"research_priority_counts"`
	Ref        EvidenceRef         `json:"evidence_ref"`
}

type PriorityCounts struct {
	P0 uint64 `json:"p0"`
	P1 uint64 `json:"p1"`
	P2 uint64 `json:"p2"`
}

// EvidenceWindow is UTC and half-open: Start <= evidence.At < End.
type EvidenceWindow struct {
	Start       time.Time     `json:"start"`
	End         time.Time     `json:"end"`
	Duration    time.Duration `json:"duration"`
	SampleCount uint64        `json:"sample_count"`
	MinSamples  uint64        `json:"min_samples"`
	Evidence    []Evidence    `json:"evidence"`
}

type Dimension struct {
	Metric      Metric   `json:"metric"`
	Polarity    Polarity `json:"polarity"`
	Numerator   uint64   `json:"numerator"`
	Denominator uint64   `json:"denominator"`
	BPS         *uint32  `json:"bps"`
}

type CapabilityReport struct {
	Capability       Capability     `json:"capability"`
	MinSamples       uint64         `json:"min_samples"`
	MaxAge           time.Duration  `json:"max_age"`
	SampleCount      uint64         `json:"sample_count"`
	SuccessCount     uint64         `json:"success_count"`
	ValidSampleCount uint64         `json:"valid_sample_count"`
	LastAttemptAt    *time.Time     `json:"last_attempt_at"`
	LastSuccessAt    *time.Time     `json:"last_success_at"`
	Age              *time.Duration `json:"age"`
	Status           Status         `json:"status"`
	Reasons          []string       `json:"reasons,omitempty"`
}

type Report struct {
	Source                  SourceKind         `json:"source"`
	Window                  EvidenceWindow     `json:"window"`
	Dimensions              []Dimension        `json:"dimensions"`
	Capabilities            []CapabilityReport `json:"capabilities"`
	TechnicalScoreBPS       *uint32            `json:"technical_score_bps"`
	Grade                   *Grade             `json:"grade"`
	Status                  Status             `json:"status"`
	License                 LicenseStatus      `json:"license"`
	CoverageGaps            []Capability       `json:"coverage_gaps,omitempty"`
	Reasons                 []string           `json:"reasons,omitempty"`
	EvidenceRefs            []EvidenceRef      `json:"evidence_refs,omitempty"`
	LastAttemptAt           *time.Time         `json:"last_attempt_at"`
	LastSuccessAt           *time.Time         `json:"last_success_at"`
	Age                     *time.Duration     `json:"age"`
	AttemptCount            uint64             `json:"attempt_count"`
	SuccessCount            uint64             `json:"success_count"`
	CacheHitCount           uint64             `json:"cache_hit_count"`
	StaleServeCount         uint64             `json:"stale_serve_count"`
	ErrorCounts             map[Outcome]uint64 `json:"error_counts"`
	PriorityCounts          PriorityCounts     `json:"priority_counts"`
	PublicEligible          bool               `json:"public_eligible"`
	ResearchContextEligible bool               `json:"research_context_eligible"`
	DiagnosticEligible      bool               `json:"diagnostic_eligible"`
	TradeEligible           bool               `json:"trade_eligible"`
	ReferenceEligible       bool               `json:"reference_eligible"`
	MatcherEligible         bool               `json:"matcher_eligible"`
	LedgerEligible          bool               `json:"ledger_eligible"`
	Gate                    GateState          `json:"gate"`
}

type GateState struct {
	Source              SourceKind `json:"source"`
	Status              Status     `json:"status"`
	HealthyWindowStreak uint32     `json:"healthy_window_streak"`
	RecoveryRequired    uint32     `json:"recovery_required"`
	Reasons             []string   `json:"reasons,omitempty"`
}

type ReportSet struct {
	Reports []Report `json:"reports"`
}

var (
	ErrInvalidPolicy   = errors.New("invalid quality policy")
	ErrInvalidEvidence = errors.New("invalid quality evidence")
	ErrInvalidWindow   = errors.New("invalid quality evidence window")
	ErrEvidenceLimit   = errors.New("quality evidence window capacity exceeded")
)

func NewEvidenceWindow(start, end time.Time, minSamples uint64, evidence []Evidence) (EvidenceWindow, error) {
	cloned := make([]Evidence, len(evidence))
	for i := range evidence {
		cloned[i] = cloneEvidence(evidence[i])
		cloned[i].At = cloned[i].At.UTC()
	}
	w := EvidenceWindow{
		Start:       start.UTC(),
		End:         end.UTC(),
		Duration:    end.UTC().Sub(start.UTC()),
		SampleCount: uint64(len(evidence)),
		MinSamples:  minSamples,
		Evidence:    cloned,
	}
	if err := w.Validate(); err != nil {
		return EvidenceWindow{}, err
	}
	sort.SliceStable(w.Evidence, func(i, j int) bool {
		if w.Evidence[i].At.Equal(w.Evidence[j].At) {
			return w.Evidence[i].ID < w.Evidence[j].ID
		}
		return w.Evidence[i].At.Before(w.Evidence[j].At)
	})
	return w, nil
}

func (w EvidenceWindow) Validate() error {
	if w.Start.IsZero() || w.End.IsZero() || w.Start.Location() != time.UTC || w.End.Location() != time.UTC {
		return fmt.Errorf("%w: start and end must be UTC", ErrInvalidWindow)
	}
	if !w.End.After(w.Start) || w.Duration != w.End.Sub(w.Start) {
		return fmt.Errorf("%w: invalid duration", ErrInvalidWindow)
	}
	if w.MinSamples == 0 || w.SampleCount != uint64(len(w.Evidence)) {
		return fmt.Errorf("%w: invalid sample counts", ErrInvalidWindow)
	}
	for i := range w.Evidence {
		if w.Evidence[i].At.Location() != time.UTC {
			return fmt.Errorf("%w: evidence %q timestamp must be UTC", ErrInvalidWindow, w.Evidence[i].ID)
		}
		if w.Evidence[i].At.Before(w.Start) || !w.Evidence[i].At.Before(w.End) {
			return fmt.Errorf("%w: evidence %q outside [start,end)", ErrInvalidWindow, w.Evidence[i].ID)
		}
	}
	return nil
}

func cloneEvidence(in Evidence) Evidence {
	out := in
	if in.Metrics != nil {
		out.Metrics = make(map[Metric]Counters, len(in.Metrics))
		for metric, counters := range in.Metrics {
			out.Metrics[metric] = counters
		}
	}
	out.HardFaults = append([]HardFault(nil), in.HardFaults...)
	return out
}
