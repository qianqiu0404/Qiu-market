package providercontract

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	DefaultMaxFutureSkew = 2 * time.Second
	MaximumTTL           = 30 * 24 * time.Hour
	MaximumDecimalScale  = int32(38)
)

var (
	decimalPattern  = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)
	intervalPattern = regexp.MustCompile(`^([1-9][0-9]*)(s|m|h|d)$`)
)

type NormalizeOptions struct {
	Now           time.Time
	MaxFutureSkew time.Duration
}

func DefaultNormalizeOptions(now time.Time) NormalizeOptions {
	return NormalizeOptions{Now: now, MaxFutureSkew: DefaultMaxFutureSkew}
}

func NormalizeMetadata(
	input Metadata,
	expected Capability,
	now time.Time,
) (Metadata, error) {
	return NormalizeMetadataWithOptions(input, expected, DefaultNormalizeOptions(now))
}

func NormalizeMetadataWithOptions(
	input Metadata,
	expected Capability,
	options NormalizeOptions,
) (Metadata, error) {
	if options.Now.IsZero() || options.MaxFutureSkew < 0 {
		return Metadata{}, NewError(
			ErrorInvalidTime, "", "normalize_metadata",
			fmt.Errorf("valid now and non-negative future skew are required"),
		)
	}
	if input.SchemaVersion != SchemaVersion {
		return Metadata{}, NewError(
			ErrorInvalidSchema, input.Source.Provider, "normalize_metadata",
			fmt.Errorf("schema version %q is not %q", input.SchemaVersion, SchemaVersion),
		)
	}
	if !validCapability(expected) || input.Capability != expected {
		return Metadata{}, NewError(
			ErrorUnsupported, input.Source.Provider, "normalize_metadata",
			fmt.Errorf("capability %q does not match %q", input.Capability, expected),
		)
	}
	source, err := NormalizeSourceRef(input.Source)
	if err != nil {
		return Metadata{}, err
	}
	input.Source = source
	if input.ObservedAt.IsZero() || input.ReceivedAt.IsZero() {
		return Metadata{}, NewError(
			ErrorInvalidTime, source.Provider, "normalize_metadata",
			fmt.Errorf("observed_at and received_at are required"),
		)
	}
	if input.TTL <= 0 || input.TTL > MaximumTTL {
		return Metadata{}, NewError(
			ErrorInvalidTime, source.Provider, "normalize_metadata",
			fmt.Errorf("ttl must be within (0,%s]", MaximumTTL),
		)
	}
	now := options.Now.UTC()
	input.ObservedAt = input.ObservedAt.UTC()
	input.ReceivedAt = input.ReceivedAt.UTC()
	if input.ObservedAt.After(now.Add(options.MaxFutureSkew)) ||
		input.ObservedAt.After(input.ReceivedAt.Add(options.MaxFutureSkew)) {
		return Metadata{}, NewError(
			ErrorFuture, source.Provider, "normalize_metadata",
			fmt.Errorf("observed_at is in the future"),
		)
	}
	if input.ReceivedAt.After(now.Add(options.MaxFutureSkew)) {
		return Metadata{}, NewError(
			ErrorFuture, source.Provider, "normalize_metadata",
			fmt.Errorf("received_at is in the future"),
		)
	}
	if input.EventTime != nil {
		eventTime := input.EventTime.UTC()
		if eventTime.After(now.Add(options.MaxFutureSkew)) ||
			eventTime.After(input.ObservedAt.Add(options.MaxFutureSkew)) {
			return Metadata{}, NewError(
				ErrorFuture, source.Provider, "normalize_metadata",
				fmt.Errorf("event_time is in the future"),
			)
		}
		input.EventTime = &eventTime
	}
	quality, err := normalizeQualityFlags(input.Quality)
	if err != nil {
		return Metadata{}, NewError(ErrorBadPayload, source.Provider, "normalize_metadata", err)
	}
	if now.Sub(input.ObservedAt) > input.TTL {
		quality = addQuality(quality, QualityStale)
	}
	input.Quality = quality
	return input, nil
}

func NormalizeSpotTicker(
	input SpotTickerEnvelope,
	now time.Time,
) (SpotTickerEnvelope, error) {
	return NormalizeSpotTickerWithOptions(input, DefaultNormalizeOptions(now))
}

func NormalizeSpotTickerWithOptions(
	input SpotTickerEnvelope,
	options NormalizeOptions,
) (SpotTickerEnvelope, error) {
	meta, err := NormalizeMetadataWithOptions(input.Meta, CapabilitySpotTicker, options)
	if err != nil {
		return SpotTickerEnvelope{}, err
	}
	market, err := NormalizeMarket(input.Market)
	if err != nil {
		return SpotTickerEnvelope{}, err
	}
	if market.Type != MarketTypeSpot {
		return SpotTickerEnvelope{}, NewError(
			ErrorConflict, meta.Source.Provider, "normalize_spot_ticker",
			fmt.Errorf("spot ticker cannot describe %q market", market.Type),
		)
	}
	data := input.Data
	data.ProviderSymbol = strings.TrimSpace(data.ProviderSymbol)
	if data.ProviderSymbol == "" || !validOpaqueText(data.ProviderSymbol, 128) {
		return SpotTickerEnvelope{}, payloadError(
			"normalize_spot_ticker", "provider_symbol", "required or invalid",
		)
	}
	data.LastPrice, err = normalizeDecimalValue(
		"last_price", data.LastPrice, []Unit{UnitQuoteAsset}, false, false,
	)
	if err != nil {
		return SpotTickerEnvelope{}, err
	}
	if data.BidPrice, err = normalizeOptionalDecimal(
		"bid_price", data.BidPrice, []Unit{UnitQuoteAsset}, false, false,
	); err != nil {
		return SpotTickerEnvelope{}, err
	}
	if data.AskPrice, err = normalizeOptionalDecimal(
		"ask_price", data.AskPrice, []Unit{UnitQuoteAsset}, false, false,
	); err != nil {
		return SpotTickerEnvelope{}, err
	}
	if (data.BidPrice == nil) != (data.AskPrice == nil) {
		meta.Quality = addQuality(meta.Quality, QualityPartial)
	}
	if data.BidPrice != nil && data.AskPrice != nil &&
		decimal.RequireFromString(data.BidPrice.Value).
			GreaterThan(decimal.RequireFromString(data.AskPrice.Value)) {
		return SpotTickerEnvelope{}, NewError(
			ErrorConflict, meta.Source.Provider, "normalize_spot_ticker",
			fmt.Errorf("bid_price exceeds ask_price"),
		)
	}
	if data.Open24h, err = normalizeOptionalDecimal(
		"open_24h", data.Open24h, []Unit{UnitQuoteAsset}, false, false,
	); err != nil {
		return SpotTickerEnvelope{}, err
	}
	if data.Change24hPct, err = normalizeOptionalDecimal(
		"change_24h_pct", data.Change24hPct, []Unit{UnitPercent}, true, true,
	); err != nil {
		return SpotTickerEnvelope{}, err
	}
	if data.QuoteTurnover, err = normalizeOptionalDecimal(
		"quote_turnover", data.QuoteTurnover, []Unit{UnitQuoteAsset}, false, true,
	); err != nil {
		return SpotTickerEnvelope{}, err
	}
	return SpotTickerEnvelope{Meta: meta, Market: market, Data: data}, nil
}

func NormalizeOHLCV(input OHLCVEnvelope, now time.Time) (OHLCVEnvelope, error) {
	return NormalizeOHLCVWithOptions(input, DefaultNormalizeOptions(now))
}

func NormalizeOHLCVWithOptions(
	input OHLCVEnvelope,
	options NormalizeOptions,
) (OHLCVEnvelope, error) {
	meta, err := NormalizeMetadataWithOptions(input.Meta, CapabilityOHLCV, options)
	if err != nil {
		return OHLCVEnvelope{}, err
	}
	market, err := NormalizeMarket(input.Market)
	if err != nil {
		return OHLCVEnvelope{}, err
	}
	interval, duration, err := normalizeInterval(input.Interval)
	if err != nil {
		return OHLCVEnvelope{}, NewError(ErrorUnit, meta.Source.Provider, "normalize_ohlcv", err)
	}
	if len(input.Data) == 0 {
		return OHLCVEnvelope{}, payloadError("normalize_ohlcv", "data", "at least one candle is required")
	}
	data := append([]OHLCV(nil), input.Data...)
	outOfOrder := false
	for index := range data {
		if index > 0 && data[index].OpenTime.Before(data[index-1].OpenTime) {
			outOfOrder = true
		}
		data[index], err = normalizeCandle(data[index], duration, options, meta.Source.Provider)
		if err != nil {
			return OHLCVEnvelope{}, err
		}
	}
	sort.SliceStable(data, func(i, j int) bool { return data[i].OpenTime.Before(data[j].OpenTime) })
	deduplicated := make([]OHLCV, 0, len(data))
	duplicate := false
	for _, candle := range data {
		if len(deduplicated) > 0 && candle.OpenTime.Equal(deduplicated[len(deduplicated)-1].OpenTime) {
			if candle != deduplicated[len(deduplicated)-1] {
				return OHLCVEnvelope{}, NewError(
					ErrorConflict, meta.Source.Provider, "normalize_ohlcv",
					fmt.Errorf("conflicting candle at %s", candle.OpenTime.Format(time.RFC3339Nano)),
				)
			}
			duplicate = true
			continue
		}
		deduplicated = append(deduplicated, candle)
	}
	if outOfOrder {
		meta.Quality = addQuality(meta.Quality, QualityOutOfOrder)
	}
	if duplicate {
		meta.Quality = addQuality(meta.Quality, QualityDuplicate)
	}
	eventTime := deduplicated[len(deduplicated)-1].OpenTime
	if meta.EventTime != nil && !meta.EventTime.Equal(eventTime) {
		return OHLCVEnvelope{}, NewError(
			ErrorConflict, meta.Source.Provider, "normalize_ohlcv",
			fmt.Errorf("event_time must equal the latest candle open_time"),
		)
	}
	meta.EventTime = &eventTime
	return OHLCVEnvelope{Meta: meta, Market: market, Interval: interval, Data: deduplicated}, nil
}

func NormalizeDerivativeSnapshot(
	input DerivativeSnapshotEnvelope,
	now time.Time,
) (DerivativeSnapshotEnvelope, error) {
	return NormalizeDerivativeSnapshotWithOptions(input, DefaultNormalizeOptions(now))
}

func NormalizeDerivativeSnapshotWithOptions(
	input DerivativeSnapshotEnvelope,
	options NormalizeOptions,
) (DerivativeSnapshotEnvelope, error) {
	meta, err := NormalizeMetadataWithOptions(input.Meta, CapabilityDerivatives, options)
	if err != nil {
		return DerivativeSnapshotEnvelope{}, err
	}
	market, err := NormalizeMarket(input.Market)
	if err != nil {
		return DerivativeSnapshotEnvelope{}, err
	}
	if market.Type != MarketTypePerp {
		return DerivativeSnapshotEnvelope{}, NewError(
			ErrorConflict, meta.Source.Provider, "normalize_derivatives",
			fmt.Errorf("derivative snapshot requires a perp market"),
		)
	}
	data := input.Data
	present := 0
	if data.MarkPrice, err = normalizeOptionalDecimal(
		"mark_price", data.MarkPrice, []Unit{UnitQuoteAsset}, false, false,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.MarkPrice != nil {
		present++
	}
	if data.IndexPrice, err = normalizeOptionalDecimal(
		"index_price", data.IndexPrice, []Unit{UnitQuoteAsset}, false, false,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.IndexPrice != nil {
		present++
	}
	if data.FundingRate, err = normalizeOptionalDecimal(
		"funding_rate", data.FundingRate, []Unit{UnitRatio, UnitPercent}, true, true,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.FundingRate != nil {
		present++
		if data.FundingIntervalSec == nil || *data.FundingIntervalSec <= 0 {
			return DerivativeSnapshotEnvelope{}, payloadError(
				"normalize_derivatives", "funding_interval_seconds", "positive value required with funding_rate",
			)
		}
	} else if data.FundingIntervalSec != nil {
		return DerivativeSnapshotEnvelope{}, payloadError(
			"normalize_derivatives", "funding_interval_seconds", "requires funding_rate",
		)
	}
	if data.OpenInterest, err = normalizeOptionalDecimal(
		"open_interest", data.OpenInterest,
		[]Unit{UnitContracts, UnitBaseAsset, UnitQuoteAsset, UnitUSD}, false, true,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.OpenInterest != nil {
		present++
	}
	if data.LongLiquidations, err = normalizeOptionalDecimal(
		"long_liquidations", data.LongLiquidations,
		[]Unit{UnitBaseAsset, UnitQuoteAsset, UnitUSD}, false, true,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.LongLiquidations != nil {
		present++
	}
	if data.ShortLiquidations, err = normalizeOptionalDecimal(
		"short_liquidations", data.ShortLiquidations,
		[]Unit{UnitBaseAsset, UnitQuoteAsset, UnitUSD}, false, true,
	); err != nil {
		return DerivativeSnapshotEnvelope{}, err
	} else if data.ShortLiquidations != nil {
		present++
	}
	hasLiquidations := data.LongLiquidations != nil || data.ShortLiquidations != nil
	if hasLiquidations {
		if data.LiquidationWindowSec == nil || *data.LiquidationWindowSec <= 0 {
			return DerivativeSnapshotEnvelope{}, payloadError(
				"normalize_derivatives", "liquidation_window_seconds",
				"positive value required with liquidation totals",
			)
		}
	} else if data.LiquidationWindowSec != nil {
		return DerivativeSnapshotEnvelope{}, payloadError(
			"normalize_derivatives", "liquidation_window_seconds",
			"requires long_liquidations or short_liquidations",
		)
	}
	if present == 0 {
		return DerivativeSnapshotEnvelope{}, payloadError(
			"normalize_derivatives", "data", "at least one derivative metric is required",
		)
	}
	if present < 3 {
		meta.Quality = addQuality(meta.Quality, QualityPartial)
	}
	return DerivativeSnapshotEnvelope{Meta: meta, Market: market, Data: data}, nil
}

func NormalizeSignal(input SignalEnvelope, now time.Time) (SignalEnvelope, error) {
	return NormalizeSignalWithOptions(input, DefaultNormalizeOptions(now))
}

func NormalizeSignalWithOptions(
	input SignalEnvelope,
	options NormalizeOptions,
) (SignalEnvelope, error) {
	meta, err := NormalizeMetadataWithOptions(input.Meta, CapabilitySignals, options)
	if err != nil {
		return SignalEnvelope{}, err
	}
	if meta.EventTime == nil {
		return SignalEnvelope{}, NewError(
			ErrorInvalidTime, meta.Source.Provider, "normalize_signal",
			fmt.Errorf("event_time is required"),
		)
	}
	if input.Asset == nil && input.Market == nil {
		return SignalEnvelope{}, NewError(
			ErrorInvalidIdentity, meta.Source.Provider, "normalize_signal",
			fmt.Errorf("asset or market reference is required"),
		)
	}
	var asset *Asset
	if input.Asset != nil {
		normalized, normalizeErr := NormalizeAsset(*input.Asset)
		if normalizeErr != nil {
			return SignalEnvelope{}, normalizeErr
		}
		asset = &normalized
	}
	var market *Market
	if input.Market != nil {
		normalized, normalizeErr := NormalizeMarket(*input.Market)
		if normalizeErr != nil {
			return SignalEnvelope{}, normalizeErr
		}
		market = &normalized
		if asset != nil && asset.ID != market.Base.ID {
			return SignalEnvelope{}, NewError(
				ErrorConflict, meta.Source.Provider, "normalize_signal",
				fmt.Errorf("asset reference does not match market base asset"),
			)
		}
	}
	data := input.Data
	data.EventID = strings.TrimSpace(data.EventID)
	data.Kind = strings.ToLower(strings.TrimSpace(data.Kind))
	data.Title = strings.TrimSpace(data.Title)
	data.Summary = strings.TrimSpace(data.Summary)
	data.Window = strings.TrimSpace(data.Window)
	if !validOpaqueText(data.EventID, 512) || !signalKindPattern.MatchString(data.Kind) {
		return SignalEnvelope{}, payloadError(
			"normalize_signal", "event_id/kind", "required or invalid",
		)
	}
	if len(data.Title) > 512 || len(data.Summary) > 4096 || len(data.Window) > 128 {
		return SignalEnvelope{}, payloadError(
			"normalize_signal", "text", "exceeds contract limit",
		)
	}
	switch data.Direction {
	case SignalDirectionNegative, SignalDirectionNeutral,
		SignalDirectionPositive, SignalDirectionUnknown:
	default:
		return SignalEnvelope{}, payloadError(
			"normalize_signal", "direction", "unsupported value",
		)
	}
	if data.Value, err = normalizeOptionalDecimal(
		"value", data.Value,
		[]Unit{UnitBaseAsset, UnitQuoteAsset, UnitUSD, UnitContracts, UnitCount, UnitRatio, UnitPercent, UnitScore},
		true, true,
	); err != nil {
		return SignalEnvelope{}, err
	}
	if data.Confidence, err = normalizeOptionalDecimal(
		"confidence", data.Confidence, []Unit{UnitScore}, false, true,
	); err != nil {
		return SignalEnvelope{}, err
	}
	if data.Confidence != nil {
		confidence := decimal.RequireFromString(data.Confidence.Value)
		if confidence.GreaterThan(decimal.NewFromInt(1)) {
			return SignalEnvelope{}, payloadError(
				"normalize_signal", "confidence", "must be within [0,1]",
			)
		}
	}
	return SignalEnvelope{Meta: meta, Asset: asset, Market: market, Data: data}, nil
}

// NormalizeResponse validates both the outer routing metadata and the concrete
// envelope. Their normalized metadata must be identical, preventing cache TTL
// or source provenance from drifting at the router boundary.
func NormalizeResponse(
	input Response,
	now time.Time,
	maxFutureSkew time.Duration,
) (Response, error) {
	options := NormalizeOptions{Now: now, MaxFutureSkew: maxFutureSkew}
	outer, err := NormalizeMetadataWithOptions(input.Meta, input.Capability, options)
	if err != nil {
		return Response{}, err
	}
	var inner Metadata
	switch value := input.Value.(type) {
	case SpotTickerEnvelope:
		normalized, normalizeErr := NormalizeSpotTickerWithOptions(value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = normalized, normalized.Meta
	case *SpotTickerEnvelope:
		if value == nil {
			return Response{}, payloadError("normalize_response", "value", "nil spot ticker")
		}
		normalized, normalizeErr := NormalizeSpotTickerWithOptions(*value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = &normalized, normalized.Meta
	case OHLCVEnvelope:
		normalized, normalizeErr := NormalizeOHLCVWithOptions(value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = normalized, normalized.Meta
	case *OHLCVEnvelope:
		if value == nil {
			return Response{}, payloadError("normalize_response", "value", "nil OHLCV")
		}
		normalized, normalizeErr := NormalizeOHLCVWithOptions(*value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = &normalized, normalized.Meta
	case DerivativeSnapshotEnvelope:
		normalized, normalizeErr := NormalizeDerivativeSnapshotWithOptions(value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = normalized, normalized.Meta
	case *DerivativeSnapshotEnvelope:
		if value == nil {
			return Response{}, payloadError("normalize_response", "value", "nil derivatives")
		}
		normalized, normalizeErr := NormalizeDerivativeSnapshotWithOptions(*value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = &normalized, normalized.Meta
	case SignalEnvelope:
		normalized, normalizeErr := NormalizeSignalWithOptions(value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = normalized, normalized.Meta
	case *SignalEnvelope:
		if value == nil {
			return Response{}, payloadError("normalize_response", "value", "nil signal")
		}
		normalized, normalizeErr := NormalizeSignalWithOptions(*value, options)
		if normalizeErr != nil {
			return Response{}, normalizeErr
		}
		input.Value, inner = &normalized, normalized.Meta
	default:
		return Response{}, NewError(
			ErrorBadPayload, outer.Source.Provider, "normalize_response",
			fmt.Errorf("unsupported response value %T", input.Value),
		)
	}
	if !reflect.DeepEqual(outer, inner) {
		return Response{}, NewError(
			ErrorConflict, outer.Source.Provider, "normalize_response",
			fmt.Errorf("outer metadata differs from concrete envelope metadata"),
		)
	}
	input.Meta = inner
	return input, nil
}

func ResponseFreshness(
	input Response,
	now time.Time,
	maxFutureSkew time.Duration,
) (FreshnessStatus, time.Duration, error) {
	meta, err := NormalizeMetadataWithOptions(
		input.Meta,
		input.Capability,
		NormalizeOptions{Now: now, MaxFutureSkew: maxFutureSkew},
	)
	if err != nil {
		return "", 0, err
	}
	age := now.UTC().Sub(meta.ObservedAt)
	// A provider timestamp inside the explicitly accepted clock-skew window is
	// fresh, but it must not extend the advertised TTL beyond the full TTL.
	if age < 0 {
		age = 0
	}
	if age > meta.TTL {
		return FreshnessStale, 0, nil
	}
	return FreshnessFresh, meta.TTL - age, nil
}

func normalizeCandle(
	input OHLCV,
	interval time.Duration,
	options NormalizeOptions,
	provider ProviderID,
) (OHLCV, error) {
	if input.OpenTime.IsZero() {
		return OHLCV{}, NewError(ErrorInvalidTime, provider, "normalize_ohlcv", fmt.Errorf("open_time is required"))
	}
	input.OpenTime = input.OpenTime.UTC()
	if input.OpenTime.After(options.Now.UTC().Add(options.MaxFutureSkew)) {
		return OHLCV{}, NewError(ErrorFuture, provider, "normalize_ohlcv", fmt.Errorf("open_time is in the future"))
	}
	expectedClose := input.OpenTime.Add(interval)
	if input.CloseTime.IsZero() {
		input.CloseTime = expectedClose
	} else {
		input.CloseTime = input.CloseTime.UTC()
		if !input.CloseTime.Equal(expectedClose) {
			return OHLCV{}, NewError(
				ErrorConflict, provider, "normalize_ohlcv",
				fmt.Errorf("close_time does not match interval"),
			)
		}
	}
	var err error
	if input.Open, err = normalizeDecimalValue("open", input.Open, []Unit{UnitQuoteAsset}, false, false); err != nil {
		return OHLCV{}, err
	}
	if input.High, err = normalizeDecimalValue("high", input.High, []Unit{UnitQuoteAsset}, false, false); err != nil {
		return OHLCV{}, err
	}
	if input.Low, err = normalizeDecimalValue("low", input.Low, []Unit{UnitQuoteAsset}, false, false); err != nil {
		return OHLCV{}, err
	}
	if input.Close, err = normalizeDecimalValue("close", input.Close, []Unit{UnitQuoteAsset}, false, false); err != nil {
		return OHLCV{}, err
	}
	if input.Volume, err = normalizeDecimalValue(
		"volume", input.Volume, []Unit{UnitBaseAsset, UnitQuoteAsset}, false, true,
	); err != nil {
		return OHLCV{}, err
	}
	open := decimal.RequireFromString(input.Open.Value)
	high := decimal.RequireFromString(input.High.Value)
	low := decimal.RequireFromString(input.Low.Value)
	closeValue := decimal.RequireFromString(input.Close.Value)
	if high.LessThan(decimal.Max(open, closeValue)) || low.GreaterThan(decimal.Min(open, closeValue)) || high.LessThan(low) {
		return OHLCV{}, NewError(
			ErrorConflict, provider, "normalize_ohlcv",
			fmt.Errorf("OHLC price bounds are inconsistent"),
		)
	}
	return input, nil
}

func NormalizeDecimalValue(input DecimalValue) (DecimalValue, error) {
	return normalizeDecimalValue("value", input, allUnits(), true, true)
}

func normalizeDecimalValue(
	field string,
	input DecimalValue,
	allowedUnits []Unit,
	allowNegative bool,
	allowZero bool,
) (DecimalValue, error) {
	input.Value = strings.TrimSpace(input.Value)
	if len(input.Value) == 0 || len(input.Value) > 128 || !decimalPattern.MatchString(input.Value) {
		return DecimalValue{}, payloadError("normalize_decimal", field, "invalid decimal string")
	}
	if input.Scale < 0 || input.Scale > MaximumDecimalScale {
		return DecimalValue{}, NewError(
			ErrorUnit, "", "normalize_decimal",
			fmt.Errorf("%s scale must be within [0,%d]", field, MaximumDecimalScale),
		)
	}
	if !containsUnit(allowedUnits, input.Unit) {
		return DecimalValue{}, NewError(
			ErrorUnit, "", "normalize_decimal",
			fmt.Errorf("%s has unsupported unit %q", field, input.Unit),
		)
	}
	parsed, err := decimal.NewFromString(input.Value)
	if err != nil {
		return DecimalValue{}, payloadError("normalize_decimal", field, "invalid decimal value")
	}
	if !allowNegative && parsed.IsNegative() {
		return DecimalValue{}, payloadError("normalize_decimal", field, "must be non-negative")
	}
	if !allowZero && parsed.IsZero() {
		return DecimalValue{}, payloadError("normalize_decimal", field, "must be positive")
	}
	canonical := parsed.String()
	if decimalPlaces(canonical) > input.Scale {
		return DecimalValue{}, NewError(
			ErrorUnit, "", "normalize_decimal",
			fmt.Errorf("%s exceeds declared scale %d", field, input.Scale),
		)
	}
	input.Value = canonical
	return input, nil
}

func normalizeOptionalDecimal(
	field string,
	input *DecimalValue,
	allowedUnits []Unit,
	allowNegative bool,
	allowZero bool,
) (*DecimalValue, error) {
	if input == nil {
		return nil, nil
	}
	value, err := normalizeDecimalValue(field, *input, allowedUnits, allowNegative, allowZero)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func normalizeInterval(value string) (string, time.Duration, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	parts := intervalPattern.FindStringSubmatch(normalized)
	if len(parts) != 3 {
		return "", 0, fmt.Errorf("invalid interval %q", value)
	}
	amount, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid interval %q", value)
	}
	multiplier := time.Second
	switch parts[2] {
	case "m":
		multiplier = time.Minute
	case "h":
		multiplier = time.Hour
	case "d":
		multiplier = 24 * time.Hour
	}
	if amount > int64((MaximumTTL / multiplier)) {
		return "", 0, fmt.Errorf("interval exceeds %s", MaximumTTL)
	}
	return normalized, time.Duration(amount) * multiplier, nil
}

func normalizeQualityFlags(values []QualityFlag) ([]QualityFlag, error) {
	result := make([]QualityFlag, 0, len(values))
	seen := make(map[QualityFlag]struct{}, len(values))
	for _, value := range values {
		if !validQualityFlag(value) {
			return nil, fmt.Errorf("unsupported quality flag %q", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func addQuality(values []QualityFlag, value QualityFlag) []QualityFlag {
	result, _ := normalizeQualityFlags(append(append([]QualityFlag(nil), values...), value))
	return result
}

func validQualityFlag(value QualityFlag) bool {
	switch value {
	case QualityDerived, QualityDuplicate, QualityMissing, QualityOutOfOrder,
		QualityPartial, QualityProviderGap, QualityStale:
		return true
	default:
		return false
	}
}

func containsUnit(values []Unit, target Unit) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func allUnits() []Unit {
	return []Unit{
		UnitBaseAsset, UnitQuoteAsset, UnitUSD, UnitContracts,
		UnitCount, UnitRatio, UnitPercent, UnitScore,
	}
}

func decimalPlaces(value string) int32 {
	point := strings.IndexByte(value, '.')
	if point < 0 {
		return 0
	}
	return int32(len(value) - point - 1)
}
