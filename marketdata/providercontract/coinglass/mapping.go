package coinglass

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	providerExchange   = "Binance"
	providerInstrument = "BTCUSD_PERP"
	providerSettlement = "USDT"
	marketID           = "binance-btc-usd-perp"

	openInterestSourceURL = "https://open-api-v4.coinglass.com/api/futures/open-interest/history?exchange=Binance&interval=4h&limit=2&symbol=BTCUSD_PERP&unit=usd"
	liquidationSourceURL  = "https://open-api-v4.coinglass.com/api/futures/liquidation/history?exchange=Binance&interval=4h&limit=2&symbol=BTCUSD_PERP"
)

var (
	contractIdentity = ContractIdentity{
		Exchange: providerExchange, InstrumentID: providerInstrument,
		BaseAsset: "BTC", QuoteAsset: "USD", SettlementCurrency: providerSettlement,
		MarketType: providercontract.MarketTypePerp,
	}
	btcUSDPerpMarket = providercontract.Market{
		ID: marketID, Venue: "binance",
		Base:  providercontract.Asset{ID: "bitcoin", Symbol: "BTC"},
		Quote: providercontract.Asset{ID: "usd", Symbol: "USD"},
		Type:  providercontract.MarketTypePerp,
	}
)

type openInterestHistoryPayload struct {
	Code string                   `json:"code"`
	Msg  string                   `json:"msg"`
	Data []openInterestHistoryRow `json:"data"`
}

type openInterestHistoryRow struct {
	Time  int64         `json:"time"`
	Open  decimalString `json:"open"`
	High  decimalString `json:"high"`
	Low   decimalString `json:"low"`
	Close decimalString `json:"close"`
}

type liquidationHistoryPayload struct {
	Code string                  `json:"code"`
	Msg  string                  `json:"msg"`
	Data []liquidationHistoryRow `json:"data"`
}

type liquidationHistoryRow struct {
	Time  int64         `json:"time"`
	Long  decimalString `json:"long_liquidation_usd"`
	Short decimalString `json:"short_liquidation_usd"`
}

// decimalString reflects the official history schemas: fractional values are
// JSON strings. Bare JSON numbers are rejected so a schema drift cannot pass
// through an implicit float or representation conversion.
type decimalString string

func (value *decimalString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("decimal field must be a JSON string")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("decimal string must not be empty")
	}
	*value = decimalString(text)
	return nil
}

func (value decimalString) String() string { return string(value) }

func mapOpenInterest(
	payload openInterestHistoryPayload,
	receivedAt time.Time,
) (providercontract.Response, error) {
	if err := validateEnvelope(payload.Code, payload.Msg, len(payload.Data), "open_interest_history"); err != nil {
		return providercontract.Response{}, err
	}
	rows := append([]openInterestHistoryRow(nil), payload.Data...)
	quality, err := normalizeOpenInterestRows(rows)
	if err != nil {
		return providercontract.Response{}, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Time < rows[j].Time })
	rows = dedupeOpenInterestRows(rows)
	latest := rows[len(rows)-1]
	closeValue, err := decimalNumber("close", latest.Close, providercontract.UnitUSD, false)
	if err != nil {
		return providercontract.Response{}, err
	}
	return derivativeResponse(
		receivedAt,
		latest.Time,
		"open-interest-history-4h",
		metricSourceID("open-interest", latest.Time),
		openInterestSourceURL,
		providercontract.DerivativeSnapshot{OpenInterest: &closeValue},
		quality,
	)
}

func mapLiquidation(
	payload liquidationHistoryPayload,
	receivedAt time.Time,
) (providercontract.Response, error) {
	if err := validateEnvelope(payload.Code, payload.Msg, len(payload.Data), "liquidation_history"); err != nil {
		return providercontract.Response{}, err
	}
	rows := append([]liquidationHistoryRow(nil), payload.Data...)
	quality, err := normalizeLiquidationRows(rows)
	if err != nil {
		return providercontract.Response{}, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Time < rows[j].Time })
	rows = dedupeLiquidationRows(rows)
	latest := rows[len(rows)-1]
	longValue, err := decimalNumber("long_liquidation_usd", latest.Long, providercontract.UnitUSD, false)
	if err != nil {
		return providercontract.Response{}, err
	}
	shortValue, err := decimalNumber("short_liquidation_usd", latest.Short, providercontract.UnitUSD, false)
	if err != nil {
		return providercontract.Response{}, err
	}
	window := liquidationWindow
	return derivativeResponse(
		receivedAt,
		latest.Time,
		"liquidation-history-4h",
		metricSourceID("liquidation", latest.Time),
		liquidationSourceURL,
		providercontract.DerivativeSnapshot{
			LongLiquidations: &longValue, ShortLiquidations: &shortValue,
			LiquidationWindowSec: &window,
		},
		quality,
	)
}

func derivativeResponse(
	receivedAt time.Time,
	eventMillis int64,
	sourceKey string,
	sourceID string,
	sourceURL string,
	data providercontract.DerivativeSnapshot,
	quality []providercontract.QualityFlag,
) (providercontract.Response, error) {
	if receivedAt.IsZero() {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime, sourceKey, "received time is required",
		)
	}
	if eventMillis <= 0 {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime, sourceKey, "row time must be positive milliseconds",
		)
	}
	eventTime := time.UnixMilli(eventMillis).UTC()
	if eventTime.After(receivedAt.UTC().Add(providercontract.DefaultMaxFutureSkew)) {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorFuture, sourceKey, "row time is after the response receipt time",
		)
	}
	if receivedAt.UTC().Sub(eventTime) > maximumSourceAge {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorStale, sourceKey,
			fmt.Sprintf("row time is older than the %s source-age limit", maximumSourceAge),
		)
	}
	quality = addQuality(quality, providercontract.QualityDerived)
	// NormalizeDerivativeSnapshot adds Partial when fewer than three metrics
	// are present. Add it to both inner and outer metadata up front so response
	// normalization is idempotent and the two copies cannot diverge.
	quality = addQuality(quality, providercontract.QualityPartial)
	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source: providercontract.SourceRef{
			Provider: ProviderID, Key: sourceKey, SourceID: sourceID, URL: sourceURL,
		},
		Capability: providercontract.CapabilityDerivatives,
		// CoinGlass documents row.time only as a timestamp. It is therefore
		// preserved as EventTime without guessing whether it opens or closes the
		// four-hour bucket; ObservedAt is the local HTTP receipt observation.
		ObservedAt: receivedAt.UTC(), EventTime: timePointer(eventTime), ReceivedAt: receivedAt.UTC(),
		TTL: derivativeTTL, Quality: quality,
	}
	envelope := providercontract.DerivativeSnapshotEnvelope{
		Meta: meta, Market: btcUSDPerpMarket, Data: data,
	}
	return providercontract.Response{
		Capability: providercontract.CapabilityDerivatives,
		Value:      envelope,
		Meta:       meta,
	}, nil
}

func validateEnvelope(code, message string, count int, operation string) error {
	if code != "0" {
		return mappingError(
			providercontract.ErrorBadPayload, operation,
			fmt.Sprintf("success response code is %q", code),
		)
	}
	if strings.TrimSpace(message) == "" {
		return mappingError(providercontract.ErrorBadPayload, operation, "msg is required")
	}
	if count == 0 {
		return mappingError(providercontract.ErrorBadPayload, operation, "data must not be empty")
	}
	return nil
}

func normalizeOpenInterestRows(
	rows []openInterestHistoryRow,
) ([]providercontract.QualityFlag, error) {
	quality := orderQualityOpenInterest(rows)
	seen := make(map[int64]openInterestHistoryRow, len(rows))
	for index, row := range rows {
		if row.Time <= 0 {
			return nil, rowError("open_interest_history", index, "time must be positive milliseconds")
		}
		openValue, err := decimalNumber("open", row.Open, providercontract.UnitUSD, false)
		if err != nil {
			return nil, err
		}
		highValue, err := decimalNumber("high", row.High, providercontract.UnitUSD, false)
		if err != nil {
			return nil, err
		}
		lowValue, err := decimalNumber("low", row.Low, providercontract.UnitUSD, false)
		if err != nil {
			return nil, err
		}
		closeValue, err := decimalNumber("close", row.Close, providercontract.UnitUSD, false)
		if err != nil {
			return nil, err
		}
		if err := validateOHLC(openValue.Value, highValue.Value, lowValue.Value, closeValue.Value); err != nil {
			return nil, rowError("open_interest_history", index, err.Error())
		}
		if previous, ok := seen[row.Time]; ok {
			if !sameOpenInterest(previous, row) {
				return nil, mappingError(
					providercontract.ErrorConflict, "open_interest_history",
					fmt.Sprintf("conflicting rows at timestamp %d", row.Time),
				)
			}
			quality = addQuality(quality, providercontract.QualityDuplicate)
			continue
		}
		seen[row.Time] = row
	}
	return quality, nil
}

func normalizeLiquidationRows(
	rows []liquidationHistoryRow,
) ([]providercontract.QualityFlag, error) {
	quality := orderQualityLiquidation(rows)
	seen := make(map[int64]liquidationHistoryRow, len(rows))
	for index, row := range rows {
		if row.Time <= 0 {
			return nil, rowError("liquidation_history", index, "time must be positive milliseconds")
		}
		if _, err := decimalNumber("long_liquidation_usd", row.Long, providercontract.UnitUSD, false); err != nil {
			return nil, err
		}
		if _, err := decimalNumber("short_liquidation_usd", row.Short, providercontract.UnitUSD, false); err != nil {
			return nil, err
		}
		if previous, ok := seen[row.Time]; ok {
			if !sameLiquidation(previous, row) {
				return nil, mappingError(
					providercontract.ErrorConflict, "liquidation_history",
					fmt.Sprintf("conflicting rows at timestamp %d", row.Time),
				)
			}
			quality = addQuality(quality, providercontract.QualityDuplicate)
			continue
		}
		seen[row.Time] = row
	}
	return quality, nil
}

func orderQualityOpenInterest(rows []openInterestHistoryRow) []providercontract.QualityFlag {
	for index := 1; index < len(rows); index++ {
		if rows[index].Time < rows[index-1].Time {
			return []providercontract.QualityFlag{providercontract.QualityOutOfOrder}
		}
	}
	return nil
}

func orderQualityLiquidation(rows []liquidationHistoryRow) []providercontract.QualityFlag {
	for index := 1; index < len(rows); index++ {
		if rows[index].Time < rows[index-1].Time {
			return []providercontract.QualityFlag{providercontract.QualityOutOfOrder}
		}
	}
	return nil
}

func dedupeOpenInterestRows(rows []openInterestHistoryRow) []openInterestHistoryRow {
	result := rows[:0]
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].Time != row.Time {
			result = append(result, row)
		}
	}
	return result
}

func dedupeLiquidationRows(rows []liquidationHistoryRow) []liquidationHistoryRow {
	result := rows[:0]
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].Time != row.Time {
			result = append(result, row)
		}
	}
	return result
}

func sameOpenInterest(left, right openInterestHistoryRow) bool {
	return sameNumber(left.Open, right.Open) && sameNumber(left.High, right.High) &&
		sameNumber(left.Low, right.Low) && sameNumber(left.Close, right.Close)
}

func sameLiquidation(left, right liquidationHistoryRow) bool {
	return sameNumber(left.Long, right.Long) && sameNumber(left.Short, right.Short)
}

func sameNumber(left, right decimalString) bool {
	// Precision is part of the provider fact. Treating 1.0 and 1.00 as the
	// same duplicate would make the surviving Scale depend on input order.
	return left == right
}

func validateOHLC(open, high, low, close string) error {
	openValue, _ := decimal.NewFromString(open)
	highValue, _ := decimal.NewFromString(high)
	lowValue, _ := decimal.NewFromString(low)
	closeValue, _ := decimal.NewFromString(close)
	if lowValue.GreaterThan(highValue) || openValue.LessThan(lowValue) ||
		openValue.GreaterThan(highValue) || closeValue.LessThan(lowValue) ||
		closeValue.GreaterThan(highValue) {
		return errors.New("OHLC values are inconsistent")
	}
	return nil
}

func decimalNumber(
	field string,
	value decimalString,
	unit providercontract.Unit,
	allowNegative bool,
) (providercontract.DecimalValue, error) {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+" is required",
		)
	}
	scale, err := decimalScale(text)
	if err != nil {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+": "+err.Error(),
		)
	}
	if scale > providercontract.MaximumDecimalScale {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorUnit, "map_decimal",
			fmt.Sprintf("%s scale exceeds %d", field, providercontract.MaximumDecimalScale),
		)
	}
	parsed, err := decimal.NewFromString(text)
	if err != nil {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+" is invalid",
		)
	}
	if !allowNegative && parsed.IsNegative() {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+" must be non-negative",
		)
	}
	return providercontract.DecimalValue{Value: text, Unit: unit, Scale: scale}, nil
}

func decimalScale(value string) (int32, error) {
	unsigned := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	if unsigned == "" || strings.ContainsAny(unsigned, "eE") || strings.Count(unsigned, ".") > 1 {
		return 0, errors.New("invalid non-exponent decimal")
	}
	parts := strings.SplitN(unsigned, ".", 2)
	if parts[0] == "" && (len(parts) == 1 || parts[1] == "") {
		return 0, errors.New("invalid decimal")
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return 0, errors.New("invalid decimal")
			}
		}
	}
	if len(parts) == 1 {
		return 0, nil
	}
	return int32(len(parts[1])), nil
}

func metricSourceID(metric string, eventMillis int64) string {
	endpoint := openInterestHistoryPath
	if metric == "liquidation" {
		endpoint = liquidationHistoryPath
	}
	return fmt.Sprintf(
		"endpoint=%s;exchange=%s;instrument=%s;settlement=%s;time=%d",
		endpoint, providerExchange, providerInstrument, providerSettlement, eventMillis,
	)
}

func addQuality(
	quality []providercontract.QualityFlag,
	flag providercontract.QualityFlag,
) []providercontract.QualityFlag {
	for _, existing := range quality {
		if existing == flag {
			return quality
		}
	}
	return append(quality, flag)
}

func rowError(operation string, index int, message string) error {
	return mappingError(
		providercontract.ErrorBadPayload, operation,
		fmt.Sprintf("row %d: %s", index, message),
	)
}

func mappingError(kind providercontract.ErrorKind, operation, message string) error {
	return providercontract.NewError(kind, ProviderID, operation, errors.New(message))
}

func timePointer(value time.Time) *time.Time {
	result := value.UTC()
	return &result
}
