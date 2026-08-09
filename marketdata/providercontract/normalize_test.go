package providercontract

import (
	"reflect"
	"testing"
	"time"
)

func TestNormalizeSpotTickerCanonicalizesDecimalUnitAndFreshness(t *testing.T) {
	now := fixtureNow()
	input := SpotTickerEnvelope{
		Meta:   fixtureMeta(CapabilitySpotTicker, now),
		Market: fixtureMarket(MarketTypeSpot),
		Data: SpotTicker{
			LastPrice:      decimalFixture("60000.000", UnitQuoteAsset, 2),
			BidPrice:       decimalPointer("59999.90", UnitQuoteAsset, 2),
			AskPrice:       decimalPointer("60000.10", UnitQuoteAsset, 2),
			Change24hPct:   decimalPointer("-1.2500", UnitPercent, 4),
			QuoteTurnover:  decimalPointer("1200000.00", UnitQuoteAsset, 2),
			ProviderSymbol: " BTCUSDT ",
		},
	}
	got, err := NormalizeSpotTicker(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.LastPrice.Value != "60000" || got.Data.Change24hPct.Value != "-1.25" ||
		got.Data.ProviderSymbol != "BTCUSDT" || got.Market.Code != "binance:BTC/USDT:spot" {
		t.Fatalf("ticker was not canonicalized: %+v", got)
	}
	if got.Meta.Freshness(now) != FreshnessFresh {
		t.Fatalf("freshness = %q", got.Meta.Freshness(now))
	}

	input.Data.LastPrice = decimalFixture("60000.001", UnitQuoteAsset, 2)
	if _, err := NormalizeSpotTicker(input, now); kind(err) != ErrorUnit {
		t.Fatalf("precision error = %v", err)
	}
	input.Data.LastPrice = decimalFixture("60000", UnitUSD, 2)
	if _, err := NormalizeSpotTicker(input, now); kind(err) != ErrorUnit {
		t.Fatalf("unit error = %v", err)
	}
}

func TestNormalizeMetadataRejectsFutureAndMarksStale(t *testing.T) {
	now := fixtureNow()
	meta := fixtureMeta(CapabilitySignals, now)
	meta.ObservedAt = now.Add(3 * time.Second)
	meta.ReceivedAt = now
	if _, err := NormalizeMetadata(meta, CapabilitySignals, now); kind(err) != ErrorFuture {
		t.Fatalf("future error = %v", err)
	}

	meta = fixtureMeta(CapabilitySignals, now)
	meta.ObservedAt = now.Add(-2 * time.Minute)
	meta.TTL = time.Minute
	got, err := NormalizeMetadata(meta, CapabilitySignals, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Quality, []QualityFlag{QualityStale}) {
		t.Fatalf("quality = %v", got.Quality)
	}
}

func TestNormalizeOHLCVSortsDeduplicatesAndRejectsConflict(t *testing.T) {
	now := fixtureNow()
	first := candle(now.Add(-2*time.Minute), "60000", "60010", "59990", "60005", "1")
	second := candle(now.Add(-time.Minute), "60005", "60020", "60000", "60010", "2")
	input := OHLCVEnvelope{
		Meta: fixtureMeta(CapabilityOHLCV, now), Market: fixtureMarket(MarketTypeSpot),
		Interval: "1M", Data: []OHLCV{second, first, first},
	}
	got, err := NormalizeOHLCV(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 || !got.Data[0].OpenTime.Equal(first.OpenTime) ||
		!reflect.DeepEqual(got.Meta.Quality, []QualityFlag{QualityDuplicate, QualityOutOfOrder}) {
		t.Fatalf("OHLCV ordering/quality = %+v", got)
	}
	if got.Meta.EventTime == nil || !got.Meta.EventTime.Equal(second.OpenTime) {
		t.Fatalf("event time = %v", got.Meta.EventTime)
	}

	conflict := first
	conflict.Close = decimalFixture("60006", UnitQuoteAsset, 2)
	input.Data = []OHLCV{first, conflict}
	if _, err := NormalizeOHLCV(input, now); kind(err) != ErrorConflict {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
}

func TestNormalizeDerivativeOptionalMetricsAndUnits(t *testing.T) {
	now := fixtureNow()
	interval := int64(8 * 60 * 60)
	liquidationWindow := int64(60 * 60)
	input := DerivativeSnapshotEnvelope{
		Meta: fixtureMeta(CapabilityDerivatives, now), Market: fixtureMarket(MarketTypePerp),
		Data: DerivativeSnapshot{
			MarkPrice:          decimalPointer("60000", UnitQuoteAsset, 2),
			FundingRate:        decimalPointer("-0.0001", UnitRatio, 6),
			FundingIntervalSec: &interval,
			OpenInterest:       decimalPointer("12000", UnitContracts, 0),
		},
	}
	got, err := NormalizeDerivativeSnapshot(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.IndexPrice != nil || got.Data.FundingRate.Value != "-0.0001" {
		t.Fatalf("optional derivative fields changed: %+v", got.Data)
	}

	input.Data.OpenInterest = decimalPointer("1", UnitPercent, 0)
	if _, err := NormalizeDerivativeSnapshot(input, now); kind(err) != ErrorUnit {
		t.Fatalf("open-interest unit error = %v", err)
	}
	input.Data = DerivativeSnapshot{}
	if _, err := NormalizeDerivativeSnapshot(input, now); kind(err) != ErrorBadPayload {
		t.Fatalf("empty derivative payload error = %v", err)
	}

	input.Data = DerivativeSnapshot{
		LongLiquidations:     decimalPointer("125000", UnitUSD, 2),
		LiquidationWindowSec: &liquidationWindow,
	}
	got, err = NormalizeDerivativeSnapshot(input, now)
	if err != nil || got.Data.LiquidationWindowSec == nil ||
		*got.Data.LiquidationWindowSec != liquidationWindow {
		t.Fatalf("liquidation window normalization = %+v err=%v", got.Data, err)
	}
	input.Data.LiquidationWindowSec = nil
	if _, err := NormalizeDerivativeSnapshot(input, now); kind(err) != ErrorBadPayload {
		t.Fatalf("missing liquidation window error = %v", err)
	}
	input.Data = DerivativeSnapshot{LiquidationWindowSec: &liquidationWindow}
	if _, err := NormalizeDerivativeSnapshot(input, now); kind(err) != ErrorBadPayload {
		t.Fatalf("orphan liquidation window error = %v", err)
	}
}

func TestNormalizeSignalRequiresEventIdentityTimeAndCanonicalReference(t *testing.T) {
	now := fixtureNow()
	eventTime := now.Add(-time.Minute)
	meta := fixtureMeta(CapabilitySignals, now)
	meta.EventTime = &eventTime
	asset := Asset{ID: "BITCOIN", Symbol: "btc"}
	input := SignalEnvelope{
		Meta: meta, Asset: &asset,
		Data: Signal{
			EventID: "news-42", Kind: "macro.rate-decision", Title: " Decision ",
			Direction:  SignalDirectionNegative,
			Confidence: decimalPointer("0.8500", UnitScore, 4),
		},
	}
	got, err := NormalizeSignal(input, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.ID != "bitcoin" || got.Data.Confidence.Value != "0.85" ||
		got.Data.Title != "Decision" {
		t.Fatalf("signal = %+v", got)
	}
	input.Meta.EventTime = nil
	if _, err := NormalizeSignal(input, now); kind(err) != ErrorInvalidTime {
		t.Fatalf("missing event time error = %v", err)
	}
}

func TestNormalizeResponseBindsMetadataAndRemainingTTL(t *testing.T) {
	now := fixtureNow()
	envelope := SpotTickerEnvelope{
		Meta: fixtureMeta(CapabilitySpotTicker, now), Market: fixtureMarket(MarketTypeSpot),
		Data: SpotTicker{LastPrice: decimalFixture("60000", UnitQuoteAsset, 2), ProviderSymbol: "BTCUSDT"},
	}
	response := Response{Capability: CapabilitySpotTicker, Meta: envelope.Meta, Value: envelope}
	got, err := NormalizeResponse(response, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	status, remaining, err := ResponseFreshness(got, now, time.Second)
	if err != nil || status != FreshnessFresh || remaining != 29*time.Second {
		t.Fatalf("freshness = %q remaining=%s err=%v", status, remaining, err)
	}
	future := got
	future.Meta.ObservedAt = now.Add(500 * time.Millisecond)
	status, remaining, err = ResponseFreshness(future, now, time.Second)
	if err != nil || status != FreshnessFresh || remaining != future.Meta.TTL {
		t.Fatalf("skew freshness = %q remaining=%s err=%v", status, remaining, err)
	}

	response.Meta.TTL = time.Minute
	if _, err := NormalizeResponse(response, now, time.Second); kind(err) != ErrorConflict {
		t.Fatalf("metadata drift error = %v", err)
	}
}

func fixtureNow() time.Time {
	return time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
}

func fixtureMeta(capability Capability, now time.Time) Metadata {
	return Metadata{
		SchemaVersion: SchemaVersion,
		Source:        SourceRef{Provider: "binance", Key: "fixture", SourceID: "fixture-v1"},
		Capability:    capability,
		ObservedAt:    now.Add(-time.Second), ReceivedAt: now, TTL: 30 * time.Second,
	}
}

func fixtureMarket(marketType MarketType) Market {
	return Market{
		ID: "market-btc-usdt", Venue: "binance",
		Base:  Asset{ID: "bitcoin", Symbol: "BTC"},
		Quote: Asset{ID: "tether", Symbol: "USDT"},
		Type:  marketType,
	}
}

func decimalFixture(value string, unit Unit, scale int32) DecimalValue {
	return DecimalValue{Value: value, Unit: unit, Scale: scale}
}

func decimalPointer(value string, unit Unit, scale int32) *DecimalValue {
	result := decimalFixture(value, unit, scale)
	return &result
}

func candle(openTime time.Time, open, high, low, closeValue, volume string) OHLCV {
	return OHLCV{
		OpenTime: openTime,
		Open:     decimalFixture(open, UnitQuoteAsset, 2),
		High:     decimalFixture(high, UnitQuoteAsset, 2),
		Low:      decimalFixture(low, UnitQuoteAsset, 2),
		Close:    decimalFixture(closeValue, UnitQuoteAsset, 2),
		Volume:   decimalFixture(volume, UnitBaseAsset, 8),
	}
}
