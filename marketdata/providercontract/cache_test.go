package providercontract

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheExpiryAndNonPositiveTTL(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(2, clock)
	response := Response{Capability: CapabilitySpotTicker}

	cache.Put("btc", response, time.Minute)
	got, ok := cache.Get("btc")
	require.True(t, ok)
	require.Equal(t, response, got)

	clock.Advance(time.Minute)
	_, ok = cache.Get("btc")
	require.False(t, ok, "an entry expires exactly at its deadline")
	require.Zero(t, cache.Len())

	cache.Put("disabled", response, 0)
	_, ok = cache.Get("disabled")
	require.False(t, ok)
}

func TestCacheBoundedLRUAndStableKeys(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(2, clock)
	ttl := time.Hour
	cache.Put("b", Response{Capability: CapabilityOHLCV}, ttl)
	cache.Put("a", Response{Capability: CapabilitySpotTicker}, ttl)

	_, ok := cache.Get("b")
	require.True(t, ok, "touch b so a becomes least recently used")
	cache.Put("c", Response{Capability: CapabilitySignals}, ttl)

	_, ok = cache.Get("a")
	require.False(t, ok)
	require.Equal(t, []string{"b", "c"}, cache.Keys())
	require.Equal(t, 2, cache.Len())
}

func TestDisabledCacheRemainsBounded(t *testing.T) {
	cache := NewCache(-1, NewManualClock(time.Now()))
	cache.Put("key", Response{}, time.Hour)
	require.Zero(t, cache.Len())
	require.Empty(t, cache.Keys())
}

func TestCacheDefensivelyCopiesResponses(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(1, clock)
	response := Response{
		Capability: CapabilityOHLCV,
		Meta:       Metadata{Quality: []QualityFlag{QualityDerived}},
		Value: OHLCVEnvelope{
			Meta: Metadata{Quality: []QualityFlag{QualityDerived}},
			Data: []OHLCV{{OpenTime: clock.Now()}},
		},
	}
	cache.Put("ohlcv", response, time.Hour)
	response.Meta.Quality[0] = QualityMissing
	response.Value.(OHLCVEnvelope).Data[0].OpenTime = time.Time{}

	first, ok := cache.Get("ohlcv")
	require.True(t, ok)
	require.Equal(t, QualityDerived, first.Meta.Quality[0])
	firstValue := first.Value.(OHLCVEnvelope)
	require.False(t, firstValue.Data[0].OpenTime.IsZero())
	firstValue.Data[0].OpenTime = time.Time{}
	first.Meta.Quality[0] = QualityMissing

	second, ok := cache.Get("ohlcv")
	require.True(t, ok)
	require.Equal(t, QualityDerived, second.Meta.Quality[0])
	require.False(t, second.Value.(OHLCVEnvelope).Data[0].OpenTime.IsZero())
}

func TestCacheDeepCopiesOptionalMetricPointers(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(1, clock)
	bid := &DecimalValue{Value: "59999", Unit: UnitQuoteAsset, Scale: 2}
	response := Response{
		Capability: CapabilitySpotTicker,
		Value:      SpotTickerEnvelope{Data: SpotTicker{BidPrice: bid}},
	}
	cache.Put("ticker", response, time.Hour)
	bid.Value = "1"

	first, ok := cache.Get("ticker")
	require.True(t, ok)
	firstBid := first.Value.(SpotTickerEnvelope).Data.BidPrice
	require.Equal(t, "59999", firstBid.Value)
	firstBid.Value = "2"

	second, ok := cache.Get("ticker")
	require.True(t, ok)
	require.Equal(t, "59999", second.Value.(SpotTickerEnvelope).Data.BidPrice.Value)

	window := int64(3600)
	interval := int64(28800)
	derivative := Response{
		Capability: CapabilityDerivatives,
		Value: DerivativeSnapshotEnvelope{Data: DerivativeSnapshot{
			FundingIntervalSec:   &interval,
			LiquidationWindowSec: &window,
		}},
	}
	cache.Put("derivatives", derivative, time.Hour)
	window = 1
	interval = 1
	got, ok := cache.Get("derivatives")
	require.True(t, ok)
	data := got.Value.(DerivativeSnapshotEnvelope).Data
	require.Equal(t, int64(3600), *data.LiquidationWindowSec)
	require.Equal(t, int64(28800), *data.FundingIntervalSec)
	*data.LiquidationWindowSec = 2
	gotAgain, ok := cache.Get("derivatives")
	require.True(t, ok)
	require.Equal(t, int64(3600), *gotAgain.Value.(DerivativeSnapshotEnvelope).Data.LiquidationWindowSec)
}
