package qualityadapters

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/binancepublic"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/coinglass"
	"github.com/the-web3/s78-market-services/marketdata/quality"
)

// BinanceEvidence combines exactly one HTTP observation with its normalized
// dispatch result. Redistribution rights remain unconfirmed, so production and
// online evidence is deliberately LicenseUnknown and never public-eligible.
func BinanceEvidence(id string, at time.Time, observation binancepublic.Observation, result providercontract.DispatchResult, fetchErr error) (quality.Evidence, error) {
	capability, ok := mapBinanceCapability(observation.Capability)
	if !ok || observation.Provider != binancepublic.ProviderID {
		return quality.Evidence{}, fmt.Errorf("quality adapter: invalid Binance observation identity")
	}
	if result.Trace.CacheHit {
		return quality.Evidence{}, fmt.Errorf("quality adapter: cache hit cannot carry an HTTP observation")
	}
	if err := validateObservation(observation.Outcome, observation.ErrorKind, result, fetchErr, binancepublic.ProviderID, observation.Capability, at); err != nil {
		return quality.Evidence{}, err
	}
	if fetchErr == nil {
		if err := validateOfficialSource(result.Response.Meta, quality.SourceBinanceSpot, capability); err != nil {
			return quality.Evidence{}, err
		}
	}
	evidence := providerEvidence(id, quality.SourceBinanceSpot, capability, at, observation.Duration, quality.LicenseUnknown, true, result, fetchErr)
	return evidence, quality.ValidateEvidence(evidence)
}

func RecordBinance(recorder Recorder, id string, at time.Time, observation binancepublic.Observation, result providercontract.DispatchResult, fetchErr error) error {
	evidence, err := BinanceEvidence(id, at, observation, result, fetchErr)
	return record(recorder, evidence, err)
}

// CoinGlassEvidence is fixture-only until credential, plan, redistribution,
// and archival rights are explicitly approved. It is always restricted and
// non-live, even if a caller supplies a successful normalized fixture.
func CoinGlassEvidence(id string, at time.Time, observation coinglass.Observation, result providercontract.DispatchResult, fetchErr error) (quality.Evidence, error) {
	capability, ok := mapCoinGlassOperation(observation.Operation)
	if !ok || observation.Provider != coinglass.ProviderID || observation.Capability != providercontract.CapabilityDerivatives {
		return quality.Evidence{}, fmt.Errorf("quality adapter: invalid CoinGlass observation identity")
	}
	if result.Trace.CacheHit {
		return quality.Evidence{}, fmt.Errorf("quality adapter: cache hit cannot carry an HTTP observation")
	}
	if err := validateObservation(observation.Outcome, observation.ErrorKind, result, fetchErr, coinglass.ProviderID, providercontract.CapabilityDerivatives, at); err != nil {
		return quality.Evidence{}, err
	}
	if fetchErr == nil {
		if err := validateOfficialSource(result.Response.Meta, quality.SourceCoinGlassDerivative, capability); err != nil {
			return quality.Evidence{}, err
		}
	}
	evidence := providerEvidence(id, quality.SourceCoinGlassDerivative, capability, at, observation.Duration, quality.LicenseRestricted, false, result, fetchErr)
	return evidence, quality.ValidateEvidence(evidence)
}

func RecordCoinGlass(recorder Recorder, id string, at time.Time, observation coinglass.Observation, result providercontract.DispatchResult, fetchErr error) error {
	evidence, err := CoinGlassEvidence(id, at, observation, result, fetchErr)
	return record(recorder, evidence, err)
}

func BinanceCacheEvidence(id string, at time.Time, capability providercontract.Capability, result providercontract.DispatchResult) (quality.Evidence, error) {
	mapped, ok := mapBinanceCapability(capability)
	if !ok {
		return quality.Evidence{}, fmt.Errorf("quality adapter: invalid Binance cache capability")
	}
	return cacheEvidence(id, at, quality.SourceBinanceSpot, mapped, quality.LicenseUnknown, true, binancepublic.ProviderID, capability, result)
}

func RecordBinanceCache(recorder Recorder, id string, at time.Time, capability providercontract.Capability, result providercontract.DispatchResult) error {
	evidence, err := BinanceCacheEvidence(id, at, capability, result)
	return record(recorder, evidence, err)
}

func CoinGlassCacheEvidence(id string, at time.Time, operation string, result providercontract.DispatchResult) (quality.Evidence, error) {
	mapped, ok := mapCoinGlassOperation(operation)
	if !ok {
		return quality.Evidence{}, fmt.Errorf("quality adapter: invalid CoinGlass cache operation")
	}
	return cacheEvidence(id, at, quality.SourceCoinGlassDerivative, mapped, quality.LicenseRestricted, false, coinglass.ProviderID, providercontract.CapabilityDerivatives, result)
}

func RecordCoinGlassCache(recorder Recorder, id string, at time.Time, operation string, result providercontract.DispatchResult) error {
	evidence, err := CoinGlassCacheEvidence(id, at, operation, result)
	return record(recorder, evidence, err)
}

func cacheEvidence(id string, at time.Time, source quality.SourceKind, capability quality.Capability, license quality.LicenseStatus, live bool, provider providercontract.ProviderID, providerCapability providercontract.Capability, result providercontract.DispatchResult) (quality.Evidence, error) {
	if !result.Trace.CacheHit {
		return quality.Evidence{}, fmt.Errorf("quality adapter: cache evidence requires a cache-hit trace")
	}
	if err := validateSuccessfulDispatch(result, provider, providerCapability, at, true); err != nil {
		return quality.Evidence{}, err
	}
	if err := validateOfficialSource(result.Response.Meta, source, capability); err != nil {
		return quality.Evidence{}, err
	}
	evidence := quality.Evidence{
		ID: id, Source: source, Capability: capability, At: at.UTC(), Outcome: quality.OutcomeSuccess,
		License: license, Live: live, CacheHit: true,
		Ref: quality.EvidenceRef{SourceID: string(result.Response.Meta.Source.Provider) + ":" + result.Response.Meta.Source.SourceID},
	}
	return evidence, quality.ValidateEvidence(evidence)
}

func validateOfficialSource(meta providercontract.Metadata, source quality.SourceKind, capability quality.Capability) error {
	ref := meta.Source
	if meta.EventTime == nil {
		return fmt.Errorf("quality adapter: official source event time is required")
	}
	var key, prefix string
	switch source {
	case quality.SourceBinanceSpot:
		switch capability {
		case quality.CapabilitySpotTicker:
			key, prefix = "spot-ticker-24h", "BTCUSDT:"
		case quality.CapabilityOHLCV:
			key, prefix = "spot-ohlcv-1m", "BTCUSDT:"
		default:
			return fmt.Errorf("quality adapter: unsupported Binance source capability")
		}
	case quality.SourceCoinGlassDerivative:
		endpoint := ""
		switch capability {
		case quality.CapabilityOpenInterest:
			key, endpoint = "open-interest-history-4h", "/api/futures/open-interest/history"
		case quality.CapabilityLiquidation:
			key, endpoint = "liquidation-history-4h", "/api/futures/liquidation/history"
		default:
			return fmt.Errorf("quality adapter: unsupported CoinGlass source capability")
		}
		prefix = "endpoint=" + endpoint + ";exchange=Binance;instrument=BTCUSD_PERP;settlement=USDT;time="
	default:
		return fmt.Errorf("quality adapter: unsupported provider source")
	}
	if ref.Key != key || !strings.HasPrefix(ref.SourceID, prefix) {
		return fmt.Errorf("quality adapter: non-canonical official source identity")
	}
	suffix := strings.TrimPrefix(ref.SourceID, prefix)
	if source == quality.SourceBinanceSpot && capability == quality.CapabilityOHLCV {
		parts := strings.Split(suffix, ":")
		if len(parts) != 2 {
			return fmt.Errorf("quality adapter: invalid Binance OHLCV source range")
		}
		first, firstErr := strconv.ParseInt(parts[0], 10, 64)
		last, lastErr := strconv.ParseInt(parts[1], 10, 64)
		if firstErr != nil || lastErr != nil || first <= 0 || first > last || last != meta.EventTime.UnixMilli() {
			return fmt.Errorf("quality adapter: invalid Binance OHLCV source range")
		}
		return nil
	}
	if !positiveInteger(suffix) || suffix != strconv.FormatInt(meta.EventTime.UnixMilli(), 10) {
		return fmt.Errorf("quality adapter: invalid official source timestamp")
	}
	return nil
}

func positiveInteger(value string) bool {
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0
}

func validateObservation(outcome string, observedKind providercontract.ErrorKind, result providercontract.DispatchResult, fetchErr error, provider providercontract.ProviderID, capability providercontract.Capability, at time.Time) error {
	if fetchErr == nil {
		if outcome != "success" || observedKind != "" {
			return fmt.Errorf("quality adapter: successful result conflicts with HTTP observation")
		}
		return validateSuccessfulDispatch(result, provider, capability, at, false)
	}
	if outcome != "error" && outcome != "canceled" && outcome != "success" {
		return fmt.Errorf("quality adapter: failed result has invalid HTTP observation outcome")
	}
	if outcome == "error" && observedKind == "" {
		return fmt.Errorf("quality adapter: failed HTTP observation has no error kind")
	}
	if outcome == "error" {
		var providerErr *providercontract.ProviderError
		if !errors.As(fetchErr, &providerErr) || providerErr.Provider != provider || providerErr.Kind != observedKind {
			return fmt.Errorf("quality adapter: HTTP and dispatch error kinds conflict")
		}
	}
	if outcome == "success" {
		var providerErr *providercontract.ProviderError
		if !errors.As(fetchErr, &providerErr) || providerErr.Provider != provider ||
			(providerErr.Kind != providercontract.ErrorBadPayload && providerErr.Kind != providercontract.ErrorInvalidIdentity &&
				providerErr.Kind != providercontract.ErrorInvalidSchema && providerErr.Kind != providercontract.ErrorInvalidTime && providerErr.Kind != providercontract.ErrorFuture &&
				providerErr.Kind != providercontract.ErrorStale && providerErr.Kind != providercontract.ErrorUnit && providerErr.Kind != providercontract.ErrorConflict &&
				providerErr.Kind != providercontract.ErrorDuplicate && providerErr.Kind != providercontract.ErrorOutOfOrder) {
			return fmt.Errorf("quality adapter: successful HTTP observation cannot explain dispatch failure")
		}
	}
	return nil
}

func validateSuccessfulDispatch(result providercontract.DispatchResult, provider providercontract.ProviderID, capability providercontract.Capability, at time.Time, cacheHit bool) error {
	if result.Response.Capability != capability || result.Response.Meta.Capability != capability ||
		result.Response.Meta.Source.Provider != provider || result.Response.Meta.Source.SourceID == "" ||
		result.Trace.ActualProvider != provider || !reflect.DeepEqual(result.Trace.Source, result.Response.Meta.Source) {
		return fmt.Errorf("quality adapter: dispatch provider/capability identity mismatch")
	}
	if len(result.Trace.Attempts) == 0 {
		return fmt.Errorf("quality adapter: successful dispatch has no attempt trace")
	}
	for _, attempt := range result.Trace.Attempts {
		if attempt.Provider != provider || attempt.Capability != capability || attempt.ErrorKind != "" || attempt.CacheHit != cacheHit {
			return fmt.Errorf("quality adapter: dispatch attempt trace mismatch")
		}
	}
	if result.Trace.CacheHit != cacheHit {
		return fmt.Errorf("quality adapter: dispatch cache trace mismatch")
	}
	if _, err := providercontract.NewDefaultConsumer().Normalize(result.Response, at.UTC()); err != nil {
		return fmt.Errorf("quality adapter: result is not a normalized provider fact: %w", err)
	}
	return nil
}

func providerEvidence(id string, source quality.SourceKind, capability quality.Capability, at time.Time, latency time.Duration, license quality.LicenseStatus, live bool, result providercontract.DispatchResult, fetchErr error) quality.Evidence {
	evidence := quality.Evidence{
		ID: id, Source: source, Capability: capability, At: at.UTC(), Latency: latency,
		License: license, Live: live, Ref: quality.EvidenceRef{SourceID: string(source) + ":" + string(capability)},
	}
	if fetchErr != nil {
		evidence.Outcome = outcomeFromProviderError(fetchErr)
		addProviderFault(&evidence, fetchErr)
		return evidence
	}
	evidence.Ref.SourceID = string(result.Response.Meta.Source.Provider) + ":" + result.Response.Meta.Source.SourceID
	evidence.Outcome = quality.OutcomeSuccess
	evidence.CacheHit = result.Trace.CacheHit
	evidence.Metrics = successfulMetrics()
	freshness := result.Response.Meta.Freshness(at)
	switch freshness {
	case providercontract.FreshnessFresh:
		evidence.Metrics[quality.MetricFreshness] = quality.Counters{Numerator: 1, Denominator: 1}
	case providercontract.FreshnessStale:
		evidence.Outcome = quality.OutcomeStale
		evidence.Metrics[quality.MetricFreshness] = quality.Counters{Denominator: 1}
		evidence.HardFaults = append(evidence.HardFaults, quality.HardFaultStale)
	case providercontract.FreshnessFuture:
		evidence.Metrics[quality.MetricFreshness] = quality.Counters{Denominator: 1}
		evidence.HardFaults = append(evidence.HardFaults, quality.HardFaultFuture)
	}
	for _, flag := range result.Response.Meta.Quality {
		applyQualityFlag(&evidence, flag)
	}
	return evidence
}

func successfulMetrics() map[quality.Metric]quality.Counters {
	one := quality.Counters{Numerator: 1, Denominator: 1}
	return map[quality.Metric]quality.Counters{
		quality.MetricAvailability: one, quality.MetricCompleteness: one, quality.MetricSchema: one,
		quality.MetricConsistency: one, quality.MetricCoverage: one,
	}
}

func applyQualityFlag(evidence *quality.Evidence, flag providercontract.QualityFlag) {
	one := quality.Counters{Numerator: 1, Denominator: 1}
	zero := quality.Counters{Denominator: 1}
	switch flag {
	case providercontract.QualityDuplicate:
		evidence.Metrics[quality.MetricDuplicate] = one
		evidence.Metrics[quality.MetricConsistency] = zero
	case providercontract.QualityOutOfOrder:
		evidence.Metrics[quality.MetricOutOfOrder] = one
		evidence.Metrics[quality.MetricConsistency] = zero
	case providercontract.QualityMissing, providercontract.QualityPartial:
		evidence.Metrics[quality.MetricCompleteness] = zero
	case providercontract.QualityProviderGap:
		evidence.Metrics[quality.MetricCoverage] = zero
		evidence.Metrics[quality.MetricCompleteness] = zero
	case providercontract.QualityStale:
		evidence.Metrics[quality.MetricFreshness] = zero
		appendFault(evidence, quality.HardFaultStale)
	}
}

func outcomeFromProviderError(err error) quality.Outcome {
	kind, ok := providercontract.ErrorKindOf(err)
	if !ok {
		return quality.OutcomeNetwork
	}
	switch kind {
	case providercontract.ErrorRateLimit:
		return quality.OutcomeRateLimit
	case providercontract.ErrorUpstream5xx, providercontract.ErrorUnavailable:
		return quality.OutcomeUpstream5xx
	case providercontract.ErrorTimeout:
		return quality.OutcomeTimeout
	case providercontract.ErrorAuth:
		return quality.OutcomeAuth
	case providercontract.ErrorPermission:
		return quality.OutcomePermission
	case providercontract.ErrorUnconfigured:
		return quality.OutcomeUnconfigured
	case providercontract.ErrorUnsupported:
		return quality.OutcomeUnsupported
	case providercontract.ErrorStale:
		return quality.OutcomeStale
	case providercontract.ErrorNetwork:
		return quality.OutcomeNetwork
	default:
		return quality.OutcomeBadPayload
	}
}

func addProviderFault(evidence *quality.Evidence, err error) {
	kind, ok := providercontract.ErrorKindOf(err)
	if !ok {
		return
	}
	switch kind {
	case providercontract.ErrorInvalidSchema, providercontract.ErrorBadPayload:
		appendFault(evidence, quality.HardFaultSchema)
	case providercontract.ErrorInvalidIdentity:
		appendFault(evidence, quality.HardFaultIdentity)
	case providercontract.ErrorUnit:
		appendFault(evidence, quality.HardFaultUnit)
	case providercontract.ErrorConflict:
		appendFault(evidence, quality.HardFaultConflict)
	case providercontract.ErrorFuture, providercontract.ErrorInvalidTime:
		appendFault(evidence, quality.HardFaultFuture)
	case providercontract.ErrorStale:
		appendFault(evidence, quality.HardFaultStale)
	}
}

func appendFault(evidence *quality.Evidence, fault quality.HardFault) {
	for _, existing := range evidence.HardFaults {
		if existing == fault {
			return
		}
	}
	evidence.HardFaults = append(evidence.HardFaults, fault)
}

func mapBinanceCapability(capability providercontract.Capability) (quality.Capability, bool) {
	switch capability {
	case providercontract.CapabilitySpotTicker:
		return quality.CapabilitySpotTicker, true
	case providercontract.CapabilityOHLCV:
		return quality.CapabilityOHLCV, true
	default:
		return "", false
	}
}

func mapCoinGlassOperation(operation string) (quality.Capability, bool) {
	switch operation {
	case coinglass.OperationOpenInterestHistory:
		return quality.CapabilityOpenInterest, true
	case coinglass.OperationLiquidationHistory:
		return quality.CapabilityLiquidation, true
	default:
		return "", false
	}
}
