package service

import (
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func TestAggregateAssetRowsGroupsByBaseAssetIDAndKeepsVenueMarkets(t *testing.T) {
	now := time.Now()
	rows := []database.MarketReadRow{
		{
			MarketID: "binance-btc", MarketCode: "binance:BTC/USDT:spot",
			SymbolGuid: "btc-spot", SymbolName: "BTC/USDT",
			BaseAssetID: "asset-btc", BaseAsset: "BTC", BaseAssetName: "Bitcoin",
			QuoteAssetID: "asset-usdt", QuoteAsset: "USDT",
			Exchange: "Binance", MarketType: "spot",
			Price: scaled("100"), Volume: scaled("10"), MarketCap: scaled("1000"),
			UpdatedAt: now,
		},
		{
			MarketID: "hyperliquid-btc", MarketCode: "hyperliquid:BTC/USD:perp",
			SymbolGuid: "btc-perp", SymbolName: "BTC/USD",
			BaseAssetID: "asset-btc", BaseAsset: "BTC", BaseAssetName: "Bitcoin",
			QuoteAssetID: "asset-usd", QuoteAsset: "USD",
			Exchange: "Hyperliquid", MarketType: "perp",
			Price: scaled("100.5"), Volume: scaled("20"), MarketCap: "0",
			UpdatedAt: now,
		},
	}
	assets := aggregateAssetRows(
		rows,
		map[string]float64{"btc-spot": 1.25, "btc-perp": 1.5},
		map[string]bool{"binance-btc": true},
	)
	if len(assets) != 1 {
		t.Fatalf("asset count = %d; want 1", len(assets))
	}
	got := assets[0].item
	if got.AssetID != "asset-btc" || got.MarketCount != 2 {
		t.Fatalf("asset identity/count = %q/%d", got.AssetID, got.MarketCount)
	}
	if got.ReferenceMarketID != "binance-btc" {
		t.Fatalf("reference market = %q; want spot", got.ReferenceMarketID)
	}
	if got.MarketCap != "1000" {
		t.Fatalf("market cap = %q; want max 1000, not a sum", got.MarketCap)
	}
	if got.Turnover24h != "30" {
		t.Fatalf("turnover = %q; want venue sum 30", got.Turnover24h)
	}
	if got.Markets[0].MarketID != "binance-btc" || !got.Markets[0].IsReference {
		t.Fatalf("first child should be reference spot: %+v", got.Markets[0])
	}
}

func TestAggregateAssetRowsDoesNotTurnMissingChangeIntoZero(t *testing.T) {
	rows := []database.MarketReadRow{{
		MarketID: "m1", SymbolGuid: "s1", BaseAssetID: "a1", BaseAsset: "BTC",
		QuoteAssetID: "q1", QuoteAsset: "USDT", MarketType: "spot",
		Price: scaled("100"), Volume: scaled("5"), UpdatedAt: time.Now(),
	}}
	assets := aggregateAssetRows(rows, map[string]float64{}, nil)
	if assets[0].item.ChangeAvailable || assets[0].item.Change24h != "" {
		t.Fatalf("missing Redis score became a value: %+v", assets[0].item)
	}
}

func TestReferenceMarketFallsBackToHighestTurnoverWithoutSpot(t *testing.T) {
	markets := []marketCandidate{
		{item: marketItem("p1", "perp", "10", "0")},
		{item: marketItem("p2", "perp", "20", "0")},
	}
	if index := referenceMarketIndex(markets); index != 1 {
		t.Fatalf("reference index = %d; want highest-turnover perp", index)
	}
}

func TestCrossVenueSpreadAndStaleDegradation(t *testing.T) {
	now := time.Now()
	rows := []database.MarketReadRow{
		{
			MarketID: "spot", SymbolGuid: "spot-symbol", BaseAssetID: "btc", BaseAsset: "BTC",
			QuoteAssetID: "usdt", QuoteAsset: "USDT", Exchange: "Binance", MarketType: "spot",
			Price: scaled("100"), Volume: scaled("40"), MarketCap: scaled("1000"), UpdatedAt: now,
		},
		{
			MarketID: "perp", SymbolGuid: "perp-symbol", BaseAssetID: "btc", BaseAsset: "BTC",
			QuoteAssetID: "usd", QuoteAsset: "USD", Exchange: "Hyperliquid", MarketType: "perp",
			Price: scaled("100.5"), Volume: scaled("60"), UpdatedAt: now,
		},
	}
	assets := aggregateAssetRows(rows, map[string]float64{"spot-symbol": 1, "perp-symbol": 1.4}, nil)
	cross := buildCrossVenueItems(assets)
	if len(cross) != 1 || !cross[0].SpreadAvailable || cross[0].IndicativeSpreadPct != "0.5" {
		t.Fatalf("spread = %+v; want available 0.5%%", cross)
	}
	if cross[0].ChangeGapPctPoints != "0.4" {
		t.Fatalf("change gap = %q; want 0.4 percentage points", cross[0].ChangeGapPctPoints)
	}
	if cross[0].SpotTurnoverShare != "40" || cross[0].PerpTurnoverShare != "60" {
		t.Fatalf("turnover shares = %s/%s", cross[0].SpotTurnoverShare, cross[0].PerpTurnoverShare)
	}

	rows[1].UpdatedAt = now.Add(-2 * time.Minute)
	stale := buildCrossVenueItems(aggregateAssetRows(rows, nil, nil))
	if stale[0].SpreadAvailable {
		t.Fatalf("stale price should not produce a spread: %+v", stale[0])
	}
}

func TestChangeDistributionBoundaries(t *testing.T) {
	buckets := buildChangeDistribution([]float64{-10, -5, -2, 0, 2, 5, 10})
	want := []int64{0, 1, 1, 1, 1, 1, 1, 1}
	for i := range buckets {
		if buckets[i].Count != want[i] {
			t.Fatalf("bucket %s count = %d; want %d", buckets[i].Key, buckets[i].Count, want[i])
		}
	}
}

func TestMarketCapSortUsesTurnoverForZeroCapAssets(t *testing.T) {
	assets := []assetReadModel{
		{item: model.AssetDashboardItem{AssetID: "low", MarketCap: "0", Turnover24h: "10"}},
		{item: model.AssetDashboardItem{AssetID: "high", MarketCap: "0", Turnover24h: "100"}},
		{item: model.AssetDashboardItem{AssetID: "cap", MarketCap: "1", Turnover24h: "1"}},
	}
	sortAssetReadModels(assets, "market_cap", "desc")
	got := []string{assets[0].item.AssetID, assets[1].item.AssetID, assets[2].item.AssetID}
	want := []string{"cap", "high", "low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort order = %v; want %v", got, want)
		}
	}
}

func TestMomentumWindowAndCoverage(t *testing.T) {
	if window, duration, expected := momentumWindow("24H"); window != "24h" || duration != 24*time.Hour || expected != 24 {
		t.Fatalf("24h window = %q/%v/%d", window, duration, expected)
	}
	if coverage := momentumCoverage(151, 168); coverage < 89.88 || coverage > 89.89 {
		t.Fatalf("coverage = %f; want about 89.881", coverage)
	}
	if coverage := momentumCoverage(800, 720); coverage != 100 {
		t.Fatalf("coverage must clamp at 100, got %f", coverage)
	}
}

func marketItem(id, marketType, volume, cap string) model.AssetMarketItem {
	return model.AssetMarketItem{
		MarketID: id, MarketType: marketType, Volume: volume, MarketCap: cap,
	}
}

func scaled(value string) string {
	rat := decimalRat(value)
	rat.Mul(rat, decimalRat("100000000"))
	return ratDecimal(rat, 0)
}
