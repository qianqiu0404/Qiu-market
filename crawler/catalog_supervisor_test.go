package crawler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveDiscoveredMarketsRequiresApprovedProviderAliases(t *testing.T) {
	now := time.Now().UTC()
	discovered := []DiscoveredMarket{{
		SourceSymbol: "BTC-USDT", BaseAlias: "BTC", QuoteAlias: "USDT",
		MarketType: "spot", UpstreamStatus: "online", Tradable: true,
	}}
	top := map[string]struct{}{"asset-btc": {}}
	unique := map[string]string{"BTC": "asset-btc", "USDT": "asset-usdt"}

	candidates, suggestions := resolveDiscoveredMarkets(
		"coinbase", discovered, map[string]string{}, unique, top, now,
	)
	require.Equal(t, "discovered", candidates[0].ResolutionStatus)
	require.Equal(t, "base_alias_review_required", *candidates[0].RejectionReason)
	require.Len(t, suggestions, 1)
	require.Equal(t, "pending", suggestions[0].ReviewStatus)

	candidates, _ = resolveDiscoveredMarkets(
		"coinbase", discovered,
		map[string]string{"BTC": "asset-btc", "USDT": "asset-usdt"},
		unique, top, now,
	)
	require.Equal(t, "resolved", candidates[0].ResolutionStatus)
	require.Equal(t, "asset-btc", *candidates[0].BaseAssetGuid)
}

func TestResolveDiscoveredMarketsRejectsPerpAndNonUSDQuote(t *testing.T) {
	now := time.Now().UTC()
	items := []DiscoveredMarket{
		{SourceSymbol: "BTCUSDT-PERP", BaseAlias: "BTC", QuoteAlias: "USDT", MarketType: "perp", Tradable: true},
		{SourceSymbol: "BTC-EUR", BaseAlias: "BTC", QuoteAlias: "EUR", MarketType: "spot", Tradable: true},
	}
	candidates, _ := resolveDiscoveredMarkets("bybit", items, nil, nil, nil, now)
	require.Equal(t, "rejected", candidates[0].ResolutionStatus)
	require.Equal(t, "unsupported_market_type", *candidates[0].RejectionReason)
	require.Equal(t, "unsupported_quote_asset", *candidates[1].RejectionReason)
}

func TestApprovedAliasMappingsChooseOneStableExternalCodePerAsset(t *testing.T) {
	result := approvedAliasMappings("coinbase", map[string]string{
		"XBT": "asset-btc", "BTC": "asset-btc", "USDC": "asset-usdc",
	})
	require.Len(t, result, 2)
	require.Equal(t, "BTC", result[0].ExternalID)
	require.Equal(t, "USDC", result[1].ExternalID)
}
