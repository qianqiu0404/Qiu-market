package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/database"
)

func TestParseCoinbaseTickerMessagePrefersExplicitOpen(t *testing.T) {
	target := make(map[string]normalizedSpotTicker)
	parseCoinbaseTickerMessage(map[string]any{
		"product_id":             "BTC-USDC",
		"price":                  "105",
		"best_bid":               "104.9",
		"best_ask":               "105.1",
		"open_24h":               "100",
		"price_percent_chg_24_h": "999",
		"volume_24h":             "12",
		"time":                   "2026-07-24T08:00:00Z",
	}, target)

	ticker, ok := target["BTC-USDC"]
	require.True(t, ok)
	require.Equal(t, "100", ticker.Open24h)
	require.Equal(t, "999", ticker.Change24hPct)
	require.Equal(t, "1260", ticker.QuoteTurnover)
	require.NotNil(t, ticker.SourceTime)
	require.Equal(t, "ticker_event", ticker.SourceTimeKind)
}

func TestNormalizedTickerSnapshotPrefersDirectChangeAndInfersOpen(t *testing.T) {
	observedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	snapshot, err := normalizedTickerSnapshot(database.ProviderMarket{
		MarketID: "market-btc", SymbolGuid: "symbol-btc",
		ExchangeGuid: "exchange-binance", ExchangeName: "Binance",
		Provider: "binance", SymbolName: "BTC/USDT", MarketType: "spot",
	}, normalizedSpotTicker{
		SourceSymbol: "BTCUSDT", Last: "105", Change24hPct: "5",
		QuoteTurnover: "1234",
	}, observedAt)

	require.NoError(t, err)
	require.NotNil(t, snapshot.Change24hPct)
	require.Equal(t, "5", *snapshot.Change24hPct)
	require.NotNil(t, snapshot.Open24h)
	require.Equal(t, "10000000000", *snapshot.Open24h)
}

func TestNormalizedTickerSnapshotPreservesRealZeroChange(t *testing.T) {
	observedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	snapshot, err := normalizedTickerSnapshot(database.ProviderMarket{
		MarketID: "market-btc", SymbolGuid: "symbol-btc",
		ExchangeGuid: "exchange-binance", ExchangeName: "Binance",
		Provider: "binance", SymbolName: "BTC/USDT", MarketType: "spot",
	}, normalizedSpotTicker{
		SourceSymbol: "BTCUSDT", Last: "100", Change24hPct: "0",
	}, observedAt)

	require.NoError(t, err)
	require.NotNil(t, snapshot.Change24hPct)
	require.Equal(t, "0", *snapshot.Change24hPct)
	require.NotNil(t, snapshot.Open24h)
	require.Equal(t, "10000000000", *snapshot.Open24h)
}

func TestNormalizedTickerSnapshotPreservesProviderTimeSemantics(t *testing.T) {
	observedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	sourceAt := observedAt.Add(-time.Second)
	snapshot, err := normalizedTickerSnapshot(database.ProviderMarket{
		MarketID: "market-btc", SymbolGuid: "symbol-btc",
		ExchangeGuid: "exchange-binance", ExchangeName: "Binance",
		Provider: "binance", SymbolName: "BTC/USDT", MarketType: "spot",
	}, normalizedSpotTicker{
		SourceSymbol: "BTCUSDT", Last: "65000",
		QuoteTurnover: "1", SourceTime: &sourceAt,
		SourceTimeKind: "ticker_window_close",
	}, observedAt)

	require.NoError(t, err)
	require.NotNil(t, snapshot.SourceTimeKind)
	require.Equal(t, "ticker_window_close", *snapshot.SourceTimeKind)
}

func TestNormalizedTickerSnapshotKeepsUnknownChangeUnavailable(t *testing.T) {
	observedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	snapshot, err := normalizedTickerSnapshot(database.ProviderMarket{
		MarketID: "market-btc", SymbolGuid: "symbol-btc",
		ExchangeGuid: "exchange-binance", ExchangeName: "Binance",
		Provider: "binance", SymbolName: "BTC/USDT", MarketType: "spot",
	}, normalizedSpotTicker{
		SourceSymbol: "BTCUSDT", Last: "65000", Bid: "64999", Ask: "65001",
		QuoteTurnover: "1200000",
	}, observedAt)

	require.NoError(t, err)
	require.Nil(t, snapshot.Change24hPct)
	require.Nil(t, snapshot.Open24h)
	require.Equal(t, "6500000000000", snapshot.Price)
	require.Equal(t, "120000000000000", *snapshot.QuoteTurnover24h)
}

func TestTickerEvidenceCountsUniqueAssetsAndPreservesZeroChange(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	details, _ := tickerEvidenceForFeedMarkets(
		[]database.ProviderFeedMarket{
			{SourceSymbol: "BTC-USDT", AssetID: "btc"},
			{SourceSymbol: "BTC-USDC", AssetID: "btc"},
			{SourceSymbol: "ETH-USDT", AssetID: "eth"},
		},
		map[string]normalizedSpotTicker{
			"BTC-USDT": {Last: "65000", Change24hPct: "0"},
			"BTC-USDC": {Last: "65001", Open24h: "64000"},
			"ETH-USDT": {Last: "0", Open24h: ""},
		},
		now,
	)

	require.EqualValues(t, 3, details.ReceivedCount)
	require.EqualValues(t, 2, details.MatchedAssetCount)
	require.EqualValues(t, 1, details.PriceAvailableCount)
	require.EqualValues(t, 1, details.ChangeAvailableCount)
}

func TestFilterProviderMarketsByAssetIDsKeepsPreviewBoundary(t *testing.T) {
	markets := []database.ProviderMarket{
		{MarketID: "btc-usdt", BaseAssetID: "btc"},
		{MarketID: "btc-usdc", BaseAssetID: "btc"},
		{MarketID: "eth-usdt", BaseAssetID: "eth"},
		{MarketID: "sol-usdt", BaseAssetID: "sol"},
	}
	filtered := filterProviderMarketsByAssetIDs(markets, map[string]struct{}{
		"btc": {}, "eth": {},
	})
	require.Len(t, filtered, 3)
	require.Equal(t, []string{"btc-usdt", "btc-usdc", "eth-usdt"}, []string{
		filtered[0].MarketID, filtered[1].MarketID, filtered[2].MarketID,
	})
}

func TestSelectCoinbaseFallbackMarketsKeepsOneStaleProductPerAsset(t *testing.T) {
	markets := []database.ProviderMarket{
		{MarketID: "btc-usd", SourceSymbol: "BTC-USD", BaseAssetID: "btc", MarketType: "spot"},
		{MarketID: "btc-usdc", SourceSymbol: "BTC-USDC", BaseAssetID: "btc", MarketType: "spot"},
		{MarketID: "eth-usd", SourceSymbol: "ETH-USD", BaseAssetID: "eth", MarketType: "spot"},
		{MarketID: "sol-usd", SourceSymbol: "SOL-USD", BaseAssetID: "sol", MarketType: "spot"},
	}
	selected := selectCoinbaseFallbackMarkets(
		markets,
		map[string]struct{}{"btc": {}, "eth": {}, "sol": {}},
		map[string]struct{}{"eth": {}},
	)

	require.Equal(t, []string{"BTC-USD", "SOL-USD"}, []string{
		selected[0].SourceSymbol, selected[1].SourceSymbol,
	})
}

func TestFetchCoinbaseRESTProductCombinesTickerAndStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/products/BTC-USD/ticker":
			_, _ = writer.Write([]byte(`{
				"price":"105","bid":"104.9","ask":"105.1",
				"volume":"12","time":"2026-07-24T08:00:00Z"
			}`))
		case "/products/BTC-USD/stats":
			_, _ = writer.Write([]byte(`{
				"open":"100","volume":"20","last":"104"
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	ticker, err := fetchCoinbaseRESTProduct(
		context.Background(), server.Client(), server.URL, "BTC-USD",
	)

	require.NoError(t, err)
	require.Equal(t, "105", ticker.Last)
	require.Equal(t, "100", ticker.Open24h)
	require.Equal(t, "2100", ticker.QuoteTurnover)
	require.NotNil(t, ticker.SourceTime)
	require.Equal(t, "ticker_event", ticker.SourceTimeKind)
}
