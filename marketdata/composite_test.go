package marketdata

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestBuildCompositeRejectsPerpStaleAndOutlier(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	one := decimal.NewFromInt(1)
	open := decimal.NewFromInt(99)
	result := BuildComposite([]CompositeCandidate{
		candidate("binance", "spot", "100", "1000", now, &one, &open),
		candidate("coinbase", "spot", "101", "800", now, &one, &open),
		candidate("okx", "spot", "150", "500", now, &one, &open),
		candidate("bybit", "spot", "100", "500", now.Add(-31*time.Second), &one, &open),
		candidate("hyperliquid", "perp", "100", "500", now, &one, &open),
	}, now)

	require.True(t, result.Available)
	require.Equal(t, 2, result.ContributorCount)
	require.Equal(t, "medium", result.Confidence)
	require.Len(t, result.Exclusions, 3)
	require.NotNil(t, result.Change24hPct)
	require.True(t, result.PriceUSD.GreaterThan(decimal.NewFromInt(100)))
	require.True(t, result.PriceUSD.LessThan(decimal.NewFromInt(101)))
}

func TestBuildCompositeConfidenceAndUnknown(t *testing.T) {
	now := time.Now().UTC()
	one := decimal.NewFromInt(1)
	for count, confidence := range map[int]string{1: "low", 2: "medium", 3: "high"} {
		items := make([]CompositeCandidate, 0, count)
		for index := 0; index < count; index++ {
			items = append(items, candidate(
				string(rune('a'+index)), "spot", "100", "0", now, &one, nil,
			))
		}
		result := BuildComposite(items, now)
		require.True(t, result.Available)
		require.Equal(t, confidence, result.Confidence)
		require.Nil(t, result.Change24hPct, "missing open must remain unavailable")
	}

	result := BuildComposite([]CompositeCandidate{
		candidate("stale", "spot", "100", "1", now.Add(-time.Minute), &one, nil),
	}, now)
	require.False(t, result.Available)
	require.Equal(t, "unknown", result.Confidence)
}

func TestBuildCompositeExcludesMissingStableRate(t *testing.T) {
	now := time.Now().UTC()
	result := BuildComposite([]CompositeCandidate{
		candidate("binance", "spot", "100", "100", now, nil, nil),
	}, now)
	require.False(t, result.Available)
	require.Equal(t, "missing_quote_rate", result.Exclusions[0].Reason)
}

func TestBuildCompositeDerivesOpenFromProviderChangeWithoutAveragingPercentages(t *testing.T) {
	now := time.Now().UTC()
	one := decimal.NewFromInt(1)
	change := decimal.NewFromInt(5)
	item := candidate("binance", "spot", "105", "100", now, &one, nil)
	item.Change24hPct = &change

	result := BuildComposite([]CompositeCandidate{item}, now)
	require.True(t, result.Available)
	require.NotNil(t, result.Open24hUSD)
	require.Equal(t, "100", result.Open24hUSD.String())
	require.NotNil(t, result.Change24hPct)
	require.Equal(t, "5", result.Change24hPct.String())
}

func TestBuildCompositeCapsTheVenueNotEachTradingPair(t *testing.T) {
	now := time.Now().UTC()
	one := decimal.NewFromInt(1)
	open := decimal.NewFromInt(100)
	items := []CompositeCandidate{
		candidate("binance", "spot", "100", "600", now, &one, &open),
		candidate("binance", "spot", "100", "300", now, &one, &open),
		candidate("coinbase", "spot", "100", "50", now, &one, &open),
		candidate("okx", "spot", "100", "50", now, &one, &open),
	}
	items[1].MarketID = "binance-usdc"
	items[1].MarketCode = "binance:BTC/USDC:spot"

	result := BuildComposite(items, now)
	require.True(t, result.Available)
	require.Equal(t, 3, result.ContributorCount)
	require.Equal(t, "high", result.Confidence)
	weights := make(map[string]decimal.Decimal)
	for _, contributor := range result.Contributors {
		weight := decimal.RequireFromString(contributor.Weight)
		weights[contributor.Provider] = weights[contributor.Provider].Add(weight)
	}

	// Water filling keeps the final normalized venue weight at or below 40%;
	// a naive cap-then-renormalize would incorrectly produce 80/10/10.
	require.InDelta(t, 0.4, weights["binance"].InexactFloat64(), 0.000001)
	require.InDelta(t, 0.3, weights["coinbase"].InexactFloat64(), 0.000001)
	require.InDelta(t, 0.3, weights["okx"].InexactFloat64(), 0.000001)
	require.InDelta(t, 1, weights["binance"].Add(weights["coinbase"]).Add(weights["okx"]).InexactFloat64(), 0.000001)
}

func TestBuildCompositeConfidenceCountsVenuesNotQuotePairs(t *testing.T) {
	now := time.Now().UTC()
	one := decimal.NewFromInt(1)
	items := []CompositeCandidate{
		candidate("binance", "spot", "100", "100", now, &one, nil),
		candidate("binance", "spot", "100", "50", now, &one, nil),
	}
	items[1].MarketID = "binance-usdc"
	items[1].MarketCode = "binance:BTC/USDC:spot"

	result := BuildComposite(items, now)
	require.True(t, result.Available)
	require.Equal(t, 1, result.ContributorCount)
	require.Equal(t, "low", result.Confidence)
}

func candidate(
	provider, marketType, price, turnover string,
	observedAt time.Time,
	rate *decimal.Decimal,
	open *decimal.Decimal,
) CompositeCandidate {
	return CompositeCandidate{
		MarketID: provider + "-market", MarketCode: provider + ":BTC/USDT:" + marketType,
		Provider: provider, MarketType: marketType, QuoteAsset: "USDT",
		Price: decimal.RequireFromString(price), Turnover24h: decimal.RequireFromString(turnover),
		ObservedAt: observedAt, QuoteToUSD: rate, Open24h: open,
	}
}
