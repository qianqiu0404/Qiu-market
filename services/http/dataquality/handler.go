// Package dataquality exposes the read-only quality monitor. It depends only
// on quality reports and has no trading, persistence, or provider transport
// dependency.
package dataquality

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/the-web3/s78-market-services/marketdata/quality"
)

const (
	Path          = "/api/v1/data-quality/summary"
	SchemaVersion = "data-quality/v1"
)

type Reporter interface {
	Reports() (quality.ReportSet, error)
}

type clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

type Counter struct {
	Numerator   uint64  `json:"numerator"`
	Denominator uint64  `json:"denominator"`
	BPS         *uint32 `json:"bps"`
}

type CapabilityItem struct {
	Capability       quality.Capability `json:"capability"`
	MaxAgeSeconds    int64              `json:"maxAgeSeconds"`
	SampleCount      uint64             `json:"sampleCount"`
	ValidSampleCount uint64             `json:"validSampleCount"`
	MinSamples       uint64             `json:"minSamples"`
	SuccessCount     uint64             `json:"successCount"`
	LastAttemptAt    *string            `json:"lastAttemptAt"`
	LastSuccessAt    *string            `json:"lastSuccessAt"`
	AgeSeconds       *int64             `json:"ageSeconds"`
	Coverage         Counter            `json:"coverage"`
	Status           quality.Status     `json:"status"`
	Reasons          []string           `json:"reasons"`
}

type Dimension struct {
	Metric      quality.Metric   `json:"metric"`
	Polarity    quality.Polarity `json:"polarity"`
	Numerator   uint64           `json:"numerator"`
	Denominator uint64           `json:"denominator"`
	BPS         *uint32          `json:"bps"`
}

type PriorityCounts struct {
	P0 uint64 `json:"p0"`
	P1 uint64 `json:"p1"`
	P2 uint64 `json:"p2"`
}

type Gate struct {
	Status              quality.Status `json:"status"`
	HealthyWindowStreak uint32         `json:"healthyWindowStreak"`
	RecoveryRequired    uint32         `json:"recoveryRequired"`
	Reasons             []string       `json:"reasons"`
}

type Item struct {
	Source            quality.SourceKind         `json:"source"`
	SourceName        string                     `json:"sourceName"`
	Class             string                     `json:"class"`
	WindowStart       *string                    `json:"windowStart"`
	WindowEnd         *string                    `json:"windowEnd"`
	WindowSeconds     *int64                     `json:"windowSeconds"`
	SampleCount       uint64                     `json:"sampleCount"`
	MinSamples        uint64                     `json:"minSamples"`
	AttemptCount      uint64                     `json:"attemptCount"`
	SuccessCount      uint64                     `json:"successCount"`
	LastAttemptAt     *string                    `json:"lastAttemptAt"`
	LastSuccessAt     *string                    `json:"lastSuccessAt"`
	AgeSeconds        *int64                     `json:"ageSeconds"`
	Coverage          Counter                    `json:"coverage"`
	TechnicalScoreBPS *uint32                    `json:"technicalScoreBps"`
	Grade             *quality.Grade             `json:"grade"`
	Status            quality.Status             `json:"status"`
	Reasons           []string                   `json:"reasons"`
	License           quality.LicenseStatus      `json:"license"`
	PublicEligible    bool                       `json:"publicEligible"`
	TradeEligible     bool                       `json:"tradeEligible"`
	ReadOnlyUse       string                     `json:"readOnlyUse"`
	Capabilities      []CapabilityItem           `json:"capabilities"`
	Dimensions        []Dimension                `json:"dimensions"`
	ErrorCounts       map[quality.Outcome]uint64 `json:"errorCounts"`
	CacheHitCount     uint64                     `json:"cacheHitCount"`
	StaleServeCount   uint64                     `json:"staleServeCount"`
	PriorityCounts    PriorityCounts             `json:"priorityCounts"`
	Gate              Gate                       `json:"gate"`
}

type Response struct {
	SchemaVersion string  `json:"schemaVersion"`
	Status        string  `json:"status"`
	GeneratedAt   string  `json:"generatedAt"`
	Items         []Item  `json:"items"`
	Error         *string `json:"error"`
}

type Handler struct {
	reporter Reporter
	clock    clock
}

func NewHandler(reporter Reporter) *Handler {
	return &Handler{reporter: reporter, clock: wallClock{}}
}

func Mount(router chi.Router, reporter Reporter) {
	router.Get(Path, NewHandler(reporter).ServeHTTP)
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	now := time.Now().UTC()
	if h != nil && h.clock != nil {
		now = h.clock.Now().UTC()
	}
	response := Response{
		SchemaVersion: SchemaVersion,
		Status:        "unconfigured",
		GeneratedAt:   now.Format(time.RFC3339Nano),
		Items:         emptyItems(),
	}
	if h == nil || h.reporter == nil {
		writeJSON(writer, response)
		return
	}
	reports, err := h.reporter.Reports()
	if err != nil {
		message := "quality monitor is unavailable"
		response.Status = "degraded"
		response.Error = &message
		writeJSON(writer, response)
		return
	}
	response.Items = reportItems(reports, now)
	response.Status = overallStatus(response.Items)
	writeJSON(writer, response)
}

type itemPolicy struct {
	source       quality.SourceKind
	name         string
	class        string
	capabilities []quality.Capability
	readOnlyUse  string
}

var itemPolicies = []itemPolicy{
	{quality.SourceBinanceSpot, "Binance Public", "spot", []quality.Capability{
		quality.CapabilitySpotTicker, quality.CapabilityOHLCV,
	}, "market_context"},
	{quality.SourceCoinGlassDerivative, "CoinGlass", "derivatives", []quality.Capability{
		quality.CapabilityOpenInterest, quality.CapabilityLiquidation,
	}, "derivatives_context"},
	{quality.SourceXiuqiuResearch, "xiuqiu-site Market Radar", "research", []quality.Capability{
		quality.CapabilityResearchSummary, quality.CapabilityResearchEvents,
	}, "research_context"},
}

func emptyItems() []Item {
	items := make([]Item, 0, 3)
	for _, policy := range itemPolicies {
		item := Item{
			Source: policy.source, SourceName: policy.name, Class: policy.class,
			Status: quality.StatusInsufficient, Reasons: []string{"quality_monitor_unconfigured"},
			License: quality.LicenseUnknown, ReadOnlyUse: policy.readOnlyUse, TradeEligible: false,
			Capabilities: make([]CapabilityItem, 0, len(policy.capabilities)), Dimensions: []Dimension{},
			ErrorCounts: map[quality.Outcome]uint64{},
		}
		defaults := quality.DefaultPolicies()[policy.source]
		item.MinSamples = defaults.MinSamples
		item.Gate = Gate{Status: quality.StatusInsufficient, RecoveryRequired: defaults.RecoveryHealthyWindows, Reasons: []string{"quality_monitor_unconfigured"}}
		for _, capability := range policy.capabilities {
			rule := defaults.CapabilityRules[capability]
			coverage := quality.Counters{Denominator: rule.MinSamples}
			item.Capabilities = append(item.Capabilities, CapabilityItem{
				Capability: capability, MinSamples: rule.MinSamples, MaxAgeSeconds: int64(rule.MaxAge / time.Second), Status: quality.StatusInsufficient,
				Coverage: Counter{Numerator: coverage.Numerator, Denominator: coverage.Denominator, BPS: coverage.BPS()},
				Reasons:  []string{"quality_monitor_unconfigured"},
			})
		}
		items = append(items, item)
	}
	return items
}

func reportItems(reports quality.ReportSet, _ time.Time) []Item {
	items := make([]Item, 0, 3)
	for _, policy := range itemPolicies {
		report, exists := reportForSource(reports, policy.source)
		if !exists {
			item := emptyItemsForPolicy(policy, "no_quality_report")
			items = append(items, item)
			continue
		}
		items = append(items, itemFromReport(report, policy))
	}
	return items
}

func emptyItemsForPolicy(policy itemPolicy, reason string) Item {
	defaults := quality.DefaultPolicies()[policy.source]
	item := Item{
		Source: policy.source, SourceName: policy.name, Class: policy.class,
		Status: quality.StatusInsufficient, Reasons: []string{reason}, License: quality.LicenseUnknown,
		ReadOnlyUse: policy.readOnlyUse, MinSamples: defaults.MinSamples, Capabilities: make([]CapabilityItem, 0, len(policy.capabilities)),
		Dimensions: []Dimension{}, ErrorCounts: map[quality.Outcome]uint64{},
	}
	item.Gate = Gate{Status: quality.StatusInsufficient, RecoveryRequired: defaults.RecoveryHealthyWindows, Reasons: []string{reason}}
	for _, capability := range policy.capabilities {
		rule := defaults.CapabilityRules[capability]
		coverage := quality.Counters{Denominator: rule.MinSamples}
		item.Capabilities = append(item.Capabilities, CapabilityItem{
			Capability: capability, MinSamples: rule.MinSamples, MaxAgeSeconds: int64(rule.MaxAge / time.Second),
			Coverage: Counter{Numerator: coverage.Numerator, Denominator: coverage.Denominator, BPS: coverage.BPS()},
			Status:   quality.StatusInsufficient, Reasons: []string{reason},
		})
	}
	return item
}

func reportForSource(reports quality.ReportSet, source quality.SourceKind) (quality.Report, bool) {
	for _, report := range reports.Reports {
		if report.Source == source {
			return report, true
		}
	}
	return quality.Report{}, false
}

func itemFromReport(report quality.Report, policy itemPolicy) Item {
	start := report.Window.Start.UTC().Format(time.RFC3339Nano)
	end := report.Window.End.UTC().Format(time.RFC3339Nano)
	windowSeconds := int64(report.Window.Duration / time.Second)
	coverage := Counter{}
	gateStatus := report.Gate.Status
	if gateStatus == "" {
		gateStatus = report.Status
	}
	recoveryRequired := report.Gate.RecoveryRequired
	if recoveryRequired == 0 {
		recoveryRequired = quality.DefaultPolicies()[policy.source].RecoveryHealthyWindows
	}
	for _, dimension := range report.Dimensions {
		if dimension.Metric == quality.MetricCoverage {
			coverage = Counter{dimension.Numerator, dimension.Denominator, cloneBPS(dimension.BPS)}
			break
		}
	}
	item := Item{
		Source: report.Source, SourceName: policy.name, Class: policy.class,
		WindowStart: &start, WindowEnd: &end, WindowSeconds: &windowSeconds,
		SampleCount: report.Window.SampleCount, MinSamples: report.Window.MinSamples,
		AttemptCount: report.AttemptCount, SuccessCount: report.SuccessCount, Coverage: coverage,
		TechnicalScoreBPS: cloneBPS(report.TechnicalScoreBPS), Grade: cloneGrade(report.Grade),
		Status: gateStatus, Reasons: append([]string{}, report.Reasons...), License: report.License,
		PublicEligible: report.PublicEligible, TradeEligible: false, ReadOnlyUse: policy.readOnlyUse,
		Capabilities: make([]CapabilityItem, 0, len(policy.capabilities)),
		Dimensions:   make([]Dimension, 0, len(report.Dimensions)),
		ErrorCounts:  cloneErrorCounts(report.ErrorCounts), CacheHitCount: report.CacheHitCount,
		StaleServeCount: report.StaleServeCount,
		PriorityCounts:  PriorityCounts{P0: report.PriorityCounts.P0, P1: report.PriorityCounts.P1, P2: report.PriorityCounts.P2},
		Gate: Gate{Status: gateStatus, HealthyWindowStreak: report.Gate.HealthyWindowStreak,
			RecoveryRequired: recoveryRequired, Reasons: append([]string{}, report.Gate.Reasons...)},
	}
	item.LastAttemptAt = formatTimePointer(report.LastAttemptAt)
	item.LastSuccessAt = formatTimePointer(report.LastSuccessAt)
	item.AgeSeconds = durationSeconds(report.Age)
	for _, value := range report.Dimensions {
		item.Dimensions = append(item.Dimensions, Dimension{
			Metric: value.Metric, Polarity: value.Polarity,
			Numerator: value.Numerator, Denominator: value.Denominator, BPS: cloneBPS(value.BPS),
		})
	}
	for _, capability := range policy.capabilities {
		item.Capabilities = append(item.Capabilities, capabilityItem(report, capability))
	}
	slices.Sort(item.Reasons)
	item.Reasons = slices.Compact(item.Reasons)
	return item
}

func cloneErrorCounts(values map[quality.Outcome]uint64) map[quality.Outcome]uint64 {
	result := make(map[quality.Outcome]uint64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func capabilityItem(report quality.Report, capability quality.Capability) CapabilityItem {
	for _, value := range report.Capabilities {
		if value.Capability != capability {
			continue
		}
		numerator := min(value.ValidSampleCount, value.MinSamples)
		coverage := quality.Counters{Numerator: numerator, Denominator: value.MinSamples}
		return CapabilityItem{
			Capability: value.Capability, MaxAgeSeconds: int64(value.MaxAge / time.Second),
			SampleCount: value.SampleCount, ValidSampleCount: value.ValidSampleCount, MinSamples: value.MinSamples, SuccessCount: value.SuccessCount,
			LastAttemptAt: formatTimePointer(value.LastAttemptAt), LastSuccessAt: formatTimePointer(value.LastSuccessAt),
			AgeSeconds: durationSeconds(value.Age), Coverage: Counter{numerator, value.MinSamples, coverage.BPS()},
			Status: value.Status, Reasons: append([]string{}, value.Reasons...),
		}
	}
	return CapabilityItem{Capability: capability, Status: quality.StatusInsufficient, Reasons: []string{"capability_report_missing"}}
}

func formatTimePointer(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func durationSeconds(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	seconds := int64(*value / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	return &seconds
}

func cloneBPS(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneGrade(value *quality.Grade) *quality.Grade {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func overallStatus(items []Item) string {
	result := "healthy"
	for _, item := range items {
		switch item.Status {
		case quality.StatusQuarantined:
			return "quarantined"
		case quality.StatusDegraded, quality.StatusRecovering:
			result = "degraded"
		case quality.StatusInsufficient:
			if result == "healthy" {
				result = "insufficient"
			}
		}
	}
	return result
}

func writeJSON(writer http.ResponseWriter, response Response) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response)
}
