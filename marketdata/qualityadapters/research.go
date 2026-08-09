package qualityadapters

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

func ResearchSummaryEvidence(id string, at time.Time, latency time.Duration, result researchsignal.SummaryResult, fetchErr error, live bool) (quality.Evidence, error) {
	evidence, err := researchEvidence(id, quality.CapabilityResearchSummary, at, latency, result.Status, fetchErr, live)
	if err != nil {
		return quality.Evidence{}, err
	}
	if fetchErr == nil && evidence.Outcome == quality.OutcomeSuccess {
		evidence.NoData = result.Status == researchsignal.StatusEmpty
		if evidence.NoData {
			evidence.Metrics[quality.MetricCompleteness] = quality.Counters{}
			evidence.Metrics[quality.MetricConsistency] = quality.Counters{}
			evidence.Metrics[quality.MetricCoverage] = quality.Counters{}
		}
		healthy := uint64(0)
		for _, source := range result.Data.Sources {
			if source.Status == researchsignal.SourceHealthy {
				healthy++
			}
		}
		evidence.Metrics[quality.MetricResearchSource] = quality.Counters{Numerator: healthy, Denominator: uint64(len(result.Data.Sources))}
	}
	return evidence, quality.ValidateEvidence(evidence)
}

func RecordResearchSummary(recorder Recorder, id string, at time.Time, latency time.Duration, result researchsignal.SummaryResult, fetchErr error, live bool) error {
	evidence, err := ResearchSummaryEvidence(id, at, latency, result, fetchErr, live)
	return record(recorder, evidence, err)
}

func ResearchEventsEvidence(id string, at time.Time, latency time.Duration, result researchsignal.ListResult, fetchErr error, live bool) (quality.Evidence, error) {
	evidence, err := researchEvidence(id, quality.CapabilityResearchEvents, at, latency, result.Status, fetchErr, live)
	if err != nil {
		return quality.Evidence{}, err
	}
	if fetchErr != nil || evidence.Outcome != quality.OutcomeSuccess {
		return evidence, quality.ValidateEvidence(evidence)
	}
	items := result.Data.Items
	if result.Quality.OutputCount != uint64(len(items)) ||
		result.Quality.InputCount < result.Quality.OutputCount+result.Quality.DuplicateCount+2*result.Quality.ConflictCount {
		return quality.Evidence{}, fmt.Errorf("quality adapter: research list quality stats are inconsistent")
	}
	if (result.Quality.DuplicateCount > 0 || result.Quality.ConflictCount > 0) && result.Status != researchsignal.StatusPartial {
		return quality.Evidence{}, fmt.Errorf("quality adapter: deduplication/conflict requires partial status")
	}
	evidence.NoData = len(items) == 0
	if evidence.NoData {
		evidence.Metrics[quality.MetricConsistency] = quality.Counters{}
	}
	evidence.Priorities = eventPriorities(items)
	watch, invalidation, legacy, stale, sourcePresent := uint64(0), uint64(0), uint64(0), uint64(0), uint64(0)
	for _, item := range items {
		if item.WatchFor != nil {
			watch++
		}
		if item.Invalidation != nil {
			invalidation++
		}
		if item.Freshness == researchsignal.FreshnessStale {
			stale++
		}
		if item.Source == "xiuqiu-site Market Radar" && item.Provider == researchsignal.Provider &&
			strings.HasPrefix(item.SourceURL, "https://xiuqiu-site.vercel.app/market-radar/events/") {
			sourcePresent++
		}
		for _, flag := range item.QualityFlags {
			if flag == "legacy_fields_missing" {
				legacy++
			}
		}
	}
	denominator := uint64(len(items))
	outOfOrder := outOfOrderCount(items)
	evidence.Metrics[quality.MetricResearchWatch] = quality.Counters{Numerator: watch, Denominator: denominator}
	evidence.Metrics[quality.MetricResearchInvalidation] = quality.Counters{Numerator: invalidation, Denominator: denominator}
	evidence.Metrics[quality.MetricResearchLegacy] = quality.Counters{Numerator: legacy, Denominator: denominator}
	evidence.Metrics[quality.MetricResearchSource] = quality.Counters{Numerator: sourcePresent, Denominator: denominator}
	evidence.Metrics[quality.MetricResearchPriority] = quality.Counters{Numerator: evidence.Priorities.P0 + evidence.Priorities.P1 + evidence.Priorities.P2, Denominator: denominator}
	evidence.Metrics[quality.MetricFreshness] = quality.Counters{Numerator: denominator - stale, Denominator: denominator}
	evidence.Metrics[quality.MetricStale] = quality.Counters{Numerator: stale, Denominator: denominator}
	if stale > 0 {
		evidence.HardFaults = append(evidence.HardFaults, quality.HardFaultStale)
	}
	evidence.Metrics[quality.MetricDuplicate] = quality.Counters{Numerator: result.Quality.DuplicateCount, Denominator: result.Quality.InputCount}
	evidence.Metrics[quality.MetricConflict] = quality.Counters{Numerator: result.Quality.ConflictCount, Denominator: result.Quality.InputCount}
	evidence.Metrics[quality.MetricContentHashConflict] = quality.Counters{Numerator: result.Quality.ConflictCount, Denominator: result.Quality.InputCount}
	evidence.Metrics[quality.MetricOutOfOrder] = quality.Counters{Numerator: outOfOrder, Denominator: denominator}
	completeDenominator := result.Quality.OutputCount + result.Quality.ConflictCount
	completeNumerator := result.Quality.OutputCount
	if result.Data.NextCursor != nil {
		completeDenominator++
	}
	if legacy <= completeNumerator {
		completeNumerator -= legacy
	} else {
		completeNumerator = 0
	}
	evidence.Metrics[quality.MetricCompleteness] = quality.Counters{Numerator: completeNumerator, Denominator: completeDenominator}
	evidence.Metrics[quality.MetricCoverage] = quality.Counters{Numerator: result.Quality.OutputCount, Denominator: completeDenominator}
	if result.Status == researchsignal.StatusPartial || result.Quality.DuplicateCount > 0 || result.Quality.ConflictCount > 0 || outOfOrder > 0 {
		consistentDenominator := max(result.Quality.InputCount, denominator)
		faults := min(consistentDenominator, result.Quality.DuplicateCount+result.Quality.ConflictCount+outOfOrder)
		evidence.Metrics[quality.MetricConsistency] = quality.Counters{Numerator: consistentDenominator - faults, Denominator: consistentDenominator}
	}
	return evidence, quality.ValidateEvidence(evidence)
}

func RecordResearchEvents(recorder Recorder, id string, at time.Time, latency time.Duration, result researchsignal.ListResult, fetchErr error, live bool) error {
	evidence, err := ResearchEventsEvidence(id, at, latency, result, fetchErr, live)
	return record(recorder, evidence, err)
}

func researchEvidence(id string, capability quality.Capability, at time.Time, latency time.Duration, status researchsignal.Status, fetchErr error, live bool) (quality.Evidence, error) {
	evidence := quality.Evidence{
		ID: id, Source: quality.SourceXiuqiuResearch, Capability: capability, At: at.UTC(), Latency: latency,
		License: quality.LicenseUnknown, Live: live,
		// The validated immutable evidence ID makes each logical fetch auditable
		// and prevents distinct windows from collapsing to one source reference.
		Ref: quality.EvidenceRef{SourceID: "xiuqiu-site:" + string(capability) + ":" + id},
	}
	if fetchErr != nil {
		evidence.Outcome = researchOutcome(fetchErr)
		return evidence, nil
	}
	switch status {
	case researchsignal.StatusStale:
		evidence.Outcome = quality.OutcomeStale
		evidence.HardFaults = []quality.HardFault{quality.HardFaultStale}
	case researchsignal.StatusDegraded, researchsignal.StatusUnconfigured:
		evidence.Outcome = quality.OutcomeUnconfigured
	case researchsignal.StatusFresh, researchsignal.StatusEmpty, researchsignal.StatusLegacy, researchsignal.StatusPartial:
		evidence.Outcome = quality.OutcomeSuccess
		evidence.Metrics = successfulMetrics()
		evidence.Metrics[quality.MetricFreshness] = boolCounter(true)
	default:
		return quality.Evidence{}, fmt.Errorf("quality adapter: unsupported research status %q", status)
	}
	return evidence, nil
}

func researchOutcome(err error) quality.Outcome {
	var typed *researchsignal.Error
	if !errors.As(err, &typed) {
		return quality.OutcomeNetwork
	}
	switch typed.Code {
	case researchsignal.ErrorRateLimit:
		return quality.OutcomeRateLimit
	case researchsignal.ErrorTimeout:
		return quality.OutcomeTimeout
	case researchsignal.ErrorNetwork:
		return quality.OutcomeNetwork
	case researchsignal.ErrorUpstream:
		return quality.OutcomeUpstream5xx
	case researchsignal.ErrorDisabled:
		return quality.OutcomeUnconfigured
	default:
		return quality.OutcomeBadPayload
	}
}

func boolCounter(value bool) quality.Counters {
	if value {
		return quality.Counters{Numerator: 1, Denominator: 1}
	}
	return quality.Counters{Denominator: 1}
}

func eventPriorities(items []researchsignal.Signal) quality.PriorityCounts {
	var result quality.PriorityCounts
	for _, item := range items {
		switch item.Priority {
		case "P0":
			result.P0++
		case "P1":
			result.P1++
		case "P2":
			result.P2++
		}
	}
	return result
}

// The reviewed feed and its keyset cursor are PublishedAt descending and ID
// descending when timestamps tie.
// Count adjacent violations without reordering the product result.
func outOfOrderCount(items []researchsignal.Signal) uint64 {
	var count uint64
	for index := 1; index < len(items); index++ {
		previous, previousErr := time.Parse(time.RFC3339Nano, items[index-1].PublishedAt)
		current, currentErr := time.Parse(time.RFC3339Nano, items[index].PublishedAt)
		if previousErr != nil || currentErr != nil || current.After(previous) || (current.Equal(previous) && items[index].ID > items[index-1].ID) {
			count++
		}
	}
	return count
}
