package binancepublic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	providerSymbol  = "BTCUSDT"
	marketID        = "binance-btc-usdt-spot"
	tickerSourceURL = "https://data-api.binance.vision/api/v3/ticker/24hr?symbol=BTCUSDT&symbolStatus=TRADING&type=FULL"
	klinesSourceURL = "https://data-api.binance.vision/api/v3/klines?interval=1m&limit=10&symbol=BTCUSDT&timeZone=0"
)

var btcUSDTMarket = providercontract.Market{
	ID:    marketID,
	Venue: "binance",
	Base:  providercontract.Asset{ID: "bitcoin", Symbol: "BTC"},
	Quote: providercontract.Asset{ID: "tether", Symbol: "USDT"},
	Type:  providercontract.MarketTypeSpot,
}

type ticker24hPayload struct {
	Symbol             string `json:"symbol"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	WeightedAvgPrice   string `json:"weightedAvgPrice"`
	PrevClosePrice     string `json:"prevClosePrice"`
	LastPrice          string `json:"lastPrice"`
	LastQty            string `json:"lastQty"`
	BidPrice           string `json:"bidPrice"`
	BidQty             string `json:"bidQty"`
	AskPrice           string `json:"askPrice"`
	AskQty             string `json:"askQty"`
	OpenPrice          string `json:"openPrice"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
	OpenTime           int64  `json:"openTime"`
	CloseTime          int64  `json:"closeTime"`
	FirstID            int64  `json:"firstId"`
	LastID             int64  `json:"lastId"`
	Count              int64  `json:"count"`
}

// Binance kline rows are fixed 12-tuples. RawMessage keeps all money and
// volume values out of float64 even before contract normalization.
type klinePayload []json.RawMessage

func mapTicker(
	payload ticker24hPayload,
	receivedAt time.Time,
) (providercontract.Response, error) {
	if receivedAt.IsZero() {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime, "ticker_24h", "received time is required",
		)
	}
	if payload.Symbol != providerSymbol {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorConflict,
			"ticker_24h",
			fmt.Sprintf("symbol %q is not %q", payload.Symbol, providerSymbol),
		)
	}
	if payload.CloseTime <= 0 {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime, "ticker_24h", "closeTime must be positive milliseconds",
		)
	}
	if payload.OpenTime <= 0 || payload.OpenTime > payload.CloseTime ||
		payload.CloseTime-payload.OpenTime > int64(25*time.Hour/time.Millisecond) {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime,
			"ticker_24h",
			"openTime/closeTime do not describe a valid rolling window",
		)
	}
	if payload.FirstID < 0 || payload.LastID < 0 || payload.Count < 0 ||
		(payload.Count > 0 && payload.FirstID > payload.LastID) {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorBadPayload,
			"ticker_24h",
			"trade IDs and count must be non-negative and ordered",
		)
	}
	eventTime := time.UnixMilli(payload.CloseTime).UTC()
	lastPrice, err := decimalValue("lastPrice", payload.LastPrice, providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.Response{}, err
	}
	bidPrice, err := decimalPointer("bidPrice", payload.BidPrice, providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.Response{}, err
	}
	askPrice, err := decimalPointer("askPrice", payload.AskPrice, providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.Response{}, err
	}
	openPrice, err := decimalPointer("openPrice", payload.OpenPrice, providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.Response{}, err
	}
	change, err := decimalPointer("priceChangePercent", payload.PriceChangePercent, providercontract.UnitPercent)
	if err != nil {
		return providercontract.Response{}, err
	}
	turnover, err := decimalPointer("quoteVolume", payload.QuoteVolume, providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.Response{}, err
	}
	// Parse the base volume even though SpotTicker currently publishes quote
	// turnover only. This prevents a malformed official FULL schema field from
	// being silently accepted.
	if _, err := decimalValue("volume", payload.Volume, providercontract.UnitBaseAsset); err != nil {
		return providercontract.Response{}, err
	}
	for _, field := range []struct {
		name        string
		value       string
		unit        providercontract.Unit
		nonNegative bool
	}{
		{"priceChange", payload.PriceChange, providercontract.UnitQuoteAsset, false},
		{"weightedAvgPrice", payload.WeightedAvgPrice, providercontract.UnitQuoteAsset, true},
		{"prevClosePrice", payload.PrevClosePrice, providercontract.UnitQuoteAsset, true},
		{"lastQty", payload.LastQty, providercontract.UnitBaseAsset, true},
		{"bidQty", payload.BidQty, providercontract.UnitBaseAsset, true},
		{"askQty", payload.AskQty, providercontract.UnitBaseAsset, true},
		{"highPrice", payload.HighPrice, providercontract.UnitQuoteAsset, true},
		{"lowPrice", payload.LowPrice, providercontract.UnitQuoteAsset, true},
	} {
		value, err := decimalValue(field.name, field.value, field.unit)
		if err != nil {
			return providercontract.Response{}, err
		}
		if field.nonNegative && strings.HasPrefix(value.Value, "-") {
			return providercontract.Response{}, mappingError(
				providercontract.ErrorBadPayload,
				"ticker_24h",
				field.name+" must be non-negative",
			)
		}
	}

	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source: providercontract.SourceRef{
			Provider: ProviderID,
			Key:      "spot-ticker-24h",
			SourceID: providerSymbol + ":" + strconv.FormatInt(payload.CloseTime, 10),
			URL:      tickerSourceURL,
		},
		Capability: providercontract.CapabilitySpotTicker,
		ObservedAt: eventTime,
		EventTime:  timePointer(eventTime),
		ReceivedAt: receivedAt.UTC(),
		TTL:        spotTickerTTL,
	}
	envelope := providercontract.SpotTickerEnvelope{
		Meta:   meta,
		Market: btcUSDTMarket,
		Data: providercontract.SpotTicker{
			LastPrice:      lastPrice,
			BidPrice:       bidPrice,
			AskPrice:       askPrice,
			Open24h:        openPrice,
			Change24hPct:   change,
			QuoteTurnover:  turnover,
			ProviderSymbol: providerSymbol,
		},
	}
	return providercontract.Response{
		Capability: providercontract.CapabilitySpotTicker,
		Value:      envelope,
		Meta:       meta,
	}, nil
}

func mapOHLCV(
	payload []klinePayload,
	receivedAt time.Time,
) (providercontract.Response, error) {
	if receivedAt.IsZero() {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorInvalidTime, "klines", "received time is required",
		)
	}
	if len(payload) == 0 {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorBadPayload, "klines", "at least one kline is required",
		)
	}
	candles := make([]providercontract.OHLCV, 0, len(payload))
	discardedOpen := false
	for index, row := range payload {
		candle, err := mapKline(row, index)
		if err != nil {
			return providercontract.Response{}, err
		}
		// Binance closeTime is inclusive. mapKline adds one millisecond to
		// produce the contract's exclusive close. The most recent endpoint
		// response normally contains an in-progress bar, which must never be
		// published as a completed OHLC fact.
		if candle.OpenTime.After(receivedAt.UTC()) {
			return providercontract.Response{}, mappingError(
				providercontract.ErrorFuture,
				"klines",
				fmt.Sprintf("row %d opens after the response was received", index),
			)
		}
		if candle.CloseTime.After(receivedAt.UTC()) {
			discardedOpen = true
			continue
		}
		candles = append(candles, candle)
	}
	if len(candles) == 0 {
		return providercontract.Response{}, mappingError(
			providercontract.ErrorBadPayload, "klines", "response contains no closed kline",
		)
	}
	earliestOpen := candles[0].OpenTime
	latestOpen := candles[0].OpenTime
	latestClose := candles[0].CloseTime
	for _, candle := range candles[1:] {
		if candle.OpenTime.Before(earliestOpen) {
			earliestOpen = candle.OpenTime
		}
		if candle.OpenTime.After(latestOpen) {
			latestOpen = candle.OpenTime
			latestClose = candle.CloseTime
		}
	}
	quality := []providercontract.QualityFlag(nil)
	if discardedOpen {
		quality = append(quality, providercontract.QualityPartial)
	}
	firstMS := earliestOpen.UnixMilli()
	lastMS := latestOpen.UnixMilli()
	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source: providercontract.SourceRef{
			Provider: ProviderID,
			Key:      "spot-ohlcv-1m",
			SourceID: fmt.Sprintf("%s:%d:%d", providerSymbol, firstMS, lastMS),
			URL:      klinesSourceURL,
		},
		Capability: providercontract.CapabilityOHLCV,
		ObservedAt: latestClose,
		EventTime:  timePointer(latestOpen),
		ReceivedAt: receivedAt.UTC(),
		TTL:        ohlcvTTL,
		Quality:    quality,
	}
	envelope, err := providercontract.NormalizeOHLCVWithOptions(
		providercontract.OHLCVEnvelope{
			Meta: meta, Market: btcUSDTMarket, Interval: "1m", Data: candles,
		},
		providercontract.NormalizeOptions{
			Now: receivedAt.UTC(), MaxFutureSkew: providercontract.DefaultMaxFutureSkew,
		},
	)
	if err != nil {
		return providercontract.Response{}, err
	}
	return providercontract.Response{
		Capability: providercontract.CapabilityOHLCV,
		Value:      envelope,
		Meta:       envelope.Meta,
	}, nil
}

func mapKline(row klinePayload, index int) (providercontract.OHLCV, error) {
	if len(row) != 12 {
		return providercontract.OHLCV{}, mappingError(
			providercontract.ErrorBadPayload,
			"klines",
			fmt.Sprintf("row %d must contain exactly 12 fields", index),
		)
	}
	openMS, err := rawInt64("openTime", row[0])
	if err != nil || openMS <= 0 {
		return providercontract.OHLCV{}, mappingError(
			providercontract.ErrorInvalidTime, "klines", fmt.Sprintf("row %d has invalid openTime", index),
		)
	}
	closeMS, err := rawInt64("closeTime", row[6])
	if err != nil || closeMS <= 0 || closeMS == math.MaxInt64 {
		return providercontract.OHLCV{}, mappingError(
			providercontract.ErrorInvalidTime, "klines", fmt.Sprintf("row %d has invalid closeTime", index),
		)
	}
	if closeMS+1 != openMS+int64(time.Minute/time.Millisecond) {
		return providercontract.OHLCV{}, mappingError(
			providercontract.ErrorConflict,
			"klines",
			fmt.Sprintf("row %d closeTime does not match 1m interval", index),
		)
	}
	openValue, err := rawDecimal("open", row[1], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	high, err := rawDecimal("high", row[2], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	low, err := rawDecimal("low", row[3], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	closeValue, err := rawDecimal("close", row[4], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	volume, err := rawDecimal("volume", row[5], providercontract.UnitBaseAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	quoteVolume, err := rawDecimal("quoteVolume", row[7], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	tradeCount, err := rawInt64("tradeCount", row[8])
	if err != nil || tradeCount < 0 {
		return providercontract.OHLCV{}, mappingError(
			providercontract.ErrorBadPayload,
			"klines",
			fmt.Sprintf("row %d has invalid tradeCount", index),
		)
	}
	takerBase, err := rawDecimal("takerBuyBaseVolume", row[9], providercontract.UnitBaseAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	takerQuote, err := rawDecimal("takerBuyQuoteVolume", row[10], providercontract.UnitQuoteAsset)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	ignored, err := rawDecimal("ignore", row[11], providercontract.UnitCount)
	if err != nil {
		return providercontract.OHLCV{}, err
	}
	for _, extra := range []providercontract.DecimalValue{volume, quoteVolume, takerBase, takerQuote, ignored} {
		if strings.HasPrefix(extra.Value, "-") {
			return providercontract.OHLCV{}, mappingError(
				providercontract.ErrorBadPayload,
				"klines",
				fmt.Sprintf("row %d contains a negative volume/count", index),
			)
		}
	}
	return providercontract.OHLCV{
		OpenTime:  time.UnixMilli(openMS).UTC(),
		CloseTime: time.UnixMilli(closeMS).UTC().Add(time.Millisecond),
		Open:      openValue,
		High:      high,
		Low:       low,
		Close:     closeValue,
		Volume:    volume,
	}, nil
}

func rawInt64(field string, raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || bytes.Equal(raw, []byte("null")) {
		return 0, mappingError(providercontract.ErrorBadPayload, "klines", field+" is required")
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, mappingError(providercontract.ErrorBadPayload, "klines", field+" must be an integer")
	}
	return value, nil
}

func rawDecimal(
	field string,
	raw json.RawMessage,
	unit providercontract.Unit,
) (providercontract.DecimalValue, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "klines", field+" must be a decimal string",
		)
	}
	return decimalValue(field, value, unit)
}

func decimalPointer(
	field string,
	value string,
	unit providercontract.Unit,
) (*providercontract.DecimalValue, error) {
	decimal, err := decimalValue(field, value, unit)
	if err != nil {
		return nil, err
	}
	return &decimal, nil
}

func decimalValue(
	field string,
	value string,
	unit providercontract.Unit,
) (providercontract.DecimalValue, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+" is required",
		)
	}
	scale, err := decimalScale(trimmed)
	if err != nil {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorBadPayload, "map_decimal", field+": "+err.Error(),
		)
	}
	if scale > providercontract.MaximumDecimalScale {
		return providercontract.DecimalValue{}, mappingError(
			providercontract.ErrorUnit,
			"map_decimal",
			fmt.Sprintf("%s scale exceeds %d", field, providercontract.MaximumDecimalScale),
		)
	}
	return providercontract.DecimalValue{Value: trimmed, Unit: unit, Scale: scale}, nil
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

func mappingError(kind providercontract.ErrorKind, operation, message string) error {
	return providercontract.NewError(kind, ProviderID, operation, errors.New(message))
}

func timePointer(value time.Time) *time.Time {
	result := value.UTC()
	return &result
}
