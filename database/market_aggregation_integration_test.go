package database

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIntegrationDexSnapshotReplacementClearsExpiredRoute(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	assetID := "test-dex-snapshot-replacement"
	legacyAssetID := "test-dex-snapshot-legacy"
	require.NoError(t, tx.Create(&[]Asset{
		{
			Guid: assetID, AssetName: "DEX Snapshot Replacement",
			AssetSymbol: "TDSR", IsActive: true,
		},
		{
			Guid: legacyAssetID, AssetName: "DEX Snapshot Legacy",
			AssetSymbol: "TDSL", IsActive: true,
		},
	}).Error)
	store := NewMarketAggregationDB(tx)
	firstObservedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, store.UpsertAssetVenueSnapshots([]AssetVenueSnapshot{{
		AssetGuid: legacyAssetID, Provider: "uniswap", PriceKind: "dex_route",
		PriceUSD: textPointer("99"), ContributorCount: 1, MarketCount: 1,
		Confidence: "low", Quality: "low", Available: true,
		ObservedAt: firstObservedAt,
	}}))
	require.NoError(t, store.ReplaceDexVenueSnapshots([]AssetVenueSnapshot{{
		AssetGuid: assetID, Provider: "uniswap", PriceKind: "dex_route",
		PriceUSD: textPointer("123.45"), Turnover24hUSD: textPointer("1000000"),
		ContributorCount: 1, MarketCount: 2, Confidence: "high", Quality: "high",
		Available: true, ObservedAt: firstObservedAt,
		Metadata: datatypes.JSON([]byte(`{"route_key":"test-v3-route"}`)),
	}}))

	failedAt := firstObservedAt.Add(time.Minute)
	require.NoError(t, store.ReplaceDexVenueSnapshots([]AssetVenueSnapshot{{
		AssetGuid: assetID, Provider: "uniswap", PriceKind: "dex_route",
		MarketCount: 0, Confidence: "unknown", Quality: "unknown",
		Available: false, ObservedAt: failedAt,
		Metadata: datatypes.JSON(
			[]byte(`{"exclusions":[{"reason":"no_current_route"}]}`),
		),
	}}))

	var snapshot AssetVenueSnapshot
	require.NoError(t, tx.Where(
		"asset_guid = ? AND provider = ? AND price_kind = ?",
		assetID, "uniswap", "dex_route",
	).Take(&snapshot).Error)
	require.False(t, snapshot.Available)
	require.Nil(t, snapshot.PriceUSD)
	require.Nil(t, snapshot.Turnover24hUSD)
	require.Zero(t, snapshot.ContributorCount)
	require.Equal(t, "unavailable", snapshot.AvailabilityStatus)
	require.NotNil(t, snapshot.LastErrorClass)
	require.Equal(t, "no_current_route", *snapshot.LastErrorClass)
	require.NotNil(t, snapshot.LastSuccessAt)
	require.Equal(t, firstObservedAt.Unix(), snapshot.LastSuccessAt.Unix())
	require.NotNil(t, snapshot.LastAttemptAt)
	require.Equal(t, failedAt.Unix(), snapshot.LastAttemptAt.Unix())

	var legacy AssetVenueSnapshot
	require.NoError(t, tx.Where(
		"asset_guid = ? AND provider = ? AND price_kind = ?",
		legacyAssetID, "uniswap", "dex_route",
	).Take(&legacy).Error)
	require.False(t, legacy.Available)
	require.Nil(t, legacy.PriceUSD)
	require.NotNil(t, legacy.LastErrorClass)
	require.Equal(t, "selection_not_current", *legacy.LastErrorClass)
}

func TestIntegrationTop50DashboardKeepsUnpricedAssetsAndStablePages(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	now := time.Now().UTC()
	assets := make([]Asset, 0, 50)
	metrics := make([]AssetMetricCurrent, 0, 50)
	snapshots := make([]AssetVenueSnapshot, 0, 3)
	for rank := 1; rank <= 50; rank++ {
		assetID := fmt.Sprintf("test-top50-asset-%02d", rank)
		assets = append(assets, Asset{
			Guid: assetID, AssetName: fmt.Sprintf("Asset %02d", rank),
			AssetSymbol: fmt.Sprintf("T%02d", rank), IsActive: true,
		})
		metrics = append(metrics, AssetMetricCurrent{
			AssetGuid: assetID, Provider: "coingecko",
			ProviderAssetID: fmt.Sprintf("test-top50-provider-%02d", rank),
			MarketCapRank:   &rank, MarketCapUSD: textPointer(fmt.Sprintf("%d000000", 51-rank)),
			ObservedAt: now, UpdatedAt: now,
		})
		if rank <= 3 {
			snapshots = append(snapshots, AssetVenueSnapshot{
				AssetGuid: assetID, Provider: "all", PriceKind: "composite_spot",
				PriceUSD:         textPointer(fmt.Sprintf("%d", 100+rank)),
				ContributorCount: 1, MarketCount: 1, Confidence: "low", Quality: "low",
				Available: true, ObservedAt: now,
			})
		}
		if rank == 4 {
			snapshots = append(snapshots, AssetVenueSnapshot{
				AssetGuid: assetID, Provider: "all", PriceKind: "composite_spot",
				PriceUSD: textPointer("104"), ContributorCount: 1, MarketCount: 1,
				Confidence: "low", Quality: "low", Available: true,
				ObservedAt: now.Add(-31 * time.Second),
			})
		}
	}
	require.NoError(t, tx.Create(&assets).Error)
	store := NewMarketAggregationDB(tx)
	require.NoError(t, store.UpsertAssetMetrics(metrics))
	require.NoError(t, store.UpsertAssetVenueSnapshots(snapshots))
	failedAt := now.Add(time.Second)
	require.NoError(t, store.UpsertAssetVenueSnapshots([]AssetVenueSnapshot{{
		AssetGuid: "test-top50-asset-01", Provider: "all", PriceKind: "composite_spot",
		Available: false, ObservedAt: failedAt,
		Metadata: datatypes.JSON([]byte(`{"exclusions":[{"reason":"source_unavailable"}]}`)),
	}}))
	var preserved AssetVenueSnapshot
	require.NoError(t, tx.Where(
		"asset_guid = ? AND provider = ? AND price_kind = ?",
		"test-top50-asset-01", "all", "composite_spot",
	).Take(&preserved).Error)
	require.True(t, equalNumericString("101", *preserved.PriceUSD))
	require.NotNil(t, preserved.LastSuccessAt)
	require.NotNil(t, preserved.LastAttemptAt)
	require.Equal(t, now.Unix(), preserved.LastSuccessAt.Unix())
	require.Equal(t, failedAt.Unix(), preserved.LastAttemptAt.Unix())
	require.Equal(t, "source_unavailable", *preserved.LastErrorClass)

	canaryCandidates := make([]ProviderMarketCandidate, 0, 11)
	for rank := 1; rank <= 11; rank++ {
		assetID := fmt.Sprintf("test-top50-asset-%02d", rank)
		canaryCandidates = append(canaryCandidates, ProviderMarketCandidate{
			Provider: "bybit", SourceSymbol: fmt.Sprintf("T%02dUSDT", rank),
			MarketType: "spot", BaseAlias: fmt.Sprintf("T%02d", rank), QuoteAlias: "USDT",
			ResolutionStatus: "resolved", BaseAssetGuid: textPointer(assetID),
			QuoteAssetGuid: textPointer("test-top50-asset-50"),
			FirstSeenAt:    now, LastSeenAt: now,
		})
	}
	require.NoError(t, store.UpsertProviderMarketCandidates(canaryCandidates))
	selection, err := store.RefreshProviderAssetSelection("bybit", 11, "integration-test")
	require.NoError(t, err)
	require.Equal(t, 11, selection.SelectedCount)
	require.NoError(t, store.SetProviderRollout("bybit", "canary", 50, nil, nil))
	fixedCanary, state, err := store.QueryRolloutAssetIDs("bybit")
	require.NoError(t, err)
	require.Len(t, fixedCanary, 10)
	require.NotContains(t, fixedCanary, "test-top50-asset-11")
	persistedCanary, err := decodeRolloutAssetIDs(state.CanaryAssetIDs)
	require.NoError(t, err)
	require.Len(t, persistedCanary, 10)

	// Local preview widens only the effective publication boundary. It must
	// preserve the formal canary mode and its frozen ten-asset manifest.
	require.NoError(t, store.SetProviderLocalPreview("bybit", true))
	previewAssets, previewState, err := store.QueryPublishedAssetIDs("bybit")
	require.NoError(t, err)
	require.Len(t, previewAssets, 11)
	require.True(t, previewState.LocalPreviewEnabled)
	require.Equal(t, "canary", previewState.Mode)
	previewCanary, err := decodeRolloutAssetIDs(previewState.CanaryAssetIDs)
	require.NoError(t, err)
	require.Equal(t, persistedCanary, previewCanary)
	readiness, err := EvaluateProviderRolloutReadiness(&DB{
		MarketAggregation: store,
		ProviderStatus:    NewProviderStatusDB(tx),
		DWAcceptance:      NewDWAcceptanceDB(tx),
	}, "bybit", 50, now)
	require.NoError(t, err)
	require.False(t, readiness.Ready)
	require.Contains(t, readiness.Blockers,
		"local preview is active; disable it to start formal rollout observation")
	require.NoError(t, store.SetProviderLocalPreview("bybit", false))
	restoredAssets, restoredState, err := store.QueryPublishedAssetIDs("bybit")
	require.NoError(t, err)
	require.Len(t, restoredAssets, 10)
	require.False(t, restoredState.LocalPreviewEnabled)
	require.Equal(t, "canary", restoredState.Mode)

	// A later market-cap reorder must not replace an already observed canary.
	require.NoError(t, tx.Table("asset_metric_current").
		Where("asset_guid = ?", "test-top50-asset-01").
		Update("market_cap_rank", 12).Error)
	require.NoError(t, tx.Table("asset_metric_current").
		Where("asset_guid = ?", "test-top50-asset-11").
		Update("market_cap_rank", 1).Error)
	stillFixed, _, err := store.QueryRolloutAssetIDs("bybit")
	require.NoError(t, err)
	require.Equal(t, fixedCanary, stillFixed)
	selectionAfterRankChange, err := store.EnsureProviderAssetSelection(
		"bybit", 11, "rank-change-check",
	)
	require.NoError(t, err)
	require.Equal(t, selection.ActiveVersion, selectionAfterRankChange.ActiveVersion,
		"a market-cap reorder alone must not replace a healthy provider selection")

	first, total, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 17, Venue: "all",
	})
	require.NoError(t, err)
	require.EqualValues(t, 11, total)
	require.Len(t, first, 11)
	var staleRow *AssetIndexDashboardRow
	for index := range first {
		if first[index].AssetID == "test-top50-asset-04" {
			staleRow = &first[index]
			break
		}
	}
	require.NotNil(t, staleRow)
	require.Equal(t, "stale", staleRow.FreshnessStatus)
	require.True(t, staleRow.Available)
	require.NotNil(t, staleRow.Price, "stale rows retain the last successful value")
	second, _, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 2, PageSize: 17, Venue: "all",
	})
	require.NoError(t, err)
	require.Empty(t, second)
	seen := make(map[string]struct{}, 11)
	for _, row := range append(first, second...) {
		_, duplicate := seen[row.AssetID]
		require.False(t, duplicate, row.AssetID)
		seen[row.AssetID] = struct{}{}
	}

	allRows, _, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 50, Venue: "bybit", IncludeUncovered: true,
	})
	require.NoError(t, err)
	require.Len(t, allRows, 11)
	for _, row := range allRows {
		require.False(t, row.Available)
		require.Nil(t, row.Price)
	}
	summary, err := store.QueryAssetIndexSummary("all")
	require.NoError(t, err)
	require.EqualValues(t, 11, summary.AssetCount)
	require.EqualValues(t, 3, summary.PricedAssetCount)
}

func TestIntegrationProviderSelectionsBuildAUniqueUnion(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	now := time.Now().UTC()
	store := NewMarketAggregationDB(tx)
	require.NoError(t, tx.Create(&Asset{
		Guid: "test-selection-usdt", AssetName: "Selection Tether",
		AssetSymbol: "USDT", IsActive: true,
	}).Error)
	for rank := 1; rank <= 120; rank++ {
		assetID := fmt.Sprintf("test-selection-asset-%02d", rank)
		require.NoError(t, tx.Create(&Asset{
			Guid: assetID, AssetName: fmt.Sprintf("Selection Asset %02d", rank),
			AssetSymbol: fmt.Sprintf("S%02d", rank), IsActive: true,
		}).Error)
		require.NoError(t, store.UpsertAssetMetrics([]AssetMetricCurrent{{
			AssetGuid: assetID, Provider: "coingecko",
			ProviderAssetID: fmt.Sprintf("selection-provider-%02d", rank),
			MarketCapRank:   &rank, ObservedAt: now, UpdatedAt: now,
		}}))
	}
	ranges := map[string][2]int{
		"binance": {1, 50}, "coinbase": {11, 60},
		"bybit": {21, 70}, "okx": {1, 50},
		"hyperliquid": {31, 80},
	}
	for provider, limits := range ranges {
		candidates := make([]ProviderMarketCandidate, 0, 50)
		for rank := limits[0]; rank <= limits[1]; rank++ {
			assetID := fmt.Sprintf("test-selection-asset-%02d", rank)
			marketType := "spot"
			quoteAlias := "USDT"
			if provider == "hyperliquid" {
				marketType = "perp"
				quoteAlias = "USD"
			}
			candidates = append(candidates, ProviderMarketCandidate{
				Provider: provider, SourceSymbol: fmt.Sprintf("S%02dUSDT", rank),
				MarketType: marketType, BaseAlias: fmt.Sprintf("S%02d", rank),
				QuoteAlias: quoteAlias, ResolutionStatus: "resolved",
				BaseAssetGuid: &assetID, QuoteAssetGuid: textPointer("test-selection-usdt"),
				FirstSeenAt: now, LastSeenAt: now,
			})
		}
		require.NoError(t, store.UpsertProviderMarketCandidates(candidates))
		state, refreshErr := store.RefreshProviderAssetSelection(provider, 50, "integration-test")
		require.NoError(t, refreshErr)
		require.Equal(t, 50, state.SelectedCount)
	}
	for _, dex := range []struct {
		provider string
		chainID  int64
		first    int
		last     int
		offset   int
	}{
		{provider: "uniswap", chainID: 1, first: 51, last: 100, offset: 10_000},
		{provider: "pancakeswap", chainID: 56, first: 71, last: 120, offset: 20_000},
	} {
		representations := make([]AssetRepresentation, 0, 50)
		pools := make([]DexPoolCandidate, 0, 50)
		for rank := dex.first; rank <= dex.last; rank++ {
			assetID := fmt.Sprintf("test-selection-asset-%02d", rank)
			tokenAddress := fmt.Sprintf("0x%040x", dex.offset+rank)
			poolAddress := fmt.Sprintf("0x%040x", dex.offset+1000+rank)
			representations = append(representations, AssetRepresentation{
				AssetGuid: assetID, ChainID: dex.chainID,
				ContractAddress: tokenAddress, RepresentationKind: "provider_mapped",
				TokenSymbol: fmt.Sprintf("S%02d", rank), Decimals: 18,
				ReviewStatus: "approved", CreatedAt: now, UpdatedAt: now,
			})
			pools = append(pools, DexPoolCandidate{
				Provider: dex.provider, ChainID: dex.chainID, ProtocolVersion: "v3",
				PoolAddress: poolAddress, Token0Address: tokenAddress,
				Token1Address: fmt.Sprintf("0x%040x", dex.offset+50_000),
				FeeTier:       3000, ResolutionStatus: "resolved",
				FirstSeenAt: now, LastSeenAt: now,
			})
		}
		require.NoError(t, store.UpsertAssetRepresentations(representations))
		require.NoError(t, store.UpsertDexPoolCandidates(pools))
		state, refreshErr := store.RefreshProviderAssetSelection(
			dex.provider, 50, "integration-test",
		)
		require.NoError(t, refreshErr)
		require.Equal(t, 50, state.SelectedCount)
	}

	union, err := store.QuerySelectedAssetUnionIDs()
	require.NoError(t, err)
	require.Len(t, union, 120)
	rows, total, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 100, Venue: "all", Universe: "provider_union",
	})
	require.NoError(t, err)
	require.EqualValues(t, 120, total)
	require.Len(t, rows, 100)
	secondPage, secondTotal, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 2, PageSize: 100, Venue: "all", Universe: "provider_union",
	})
	require.NoError(t, err)
	require.EqualValues(t, 120, secondTotal)
	require.Len(t, secondPage, 20)
}

func TestIntegrationMarketAggregationCatalogLifecycleAndDashboard(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	require.NoError(t, tx.Create([]Asset{
		{Guid: "test-asset-btc", AssetName: "Bitcoin", AssetSymbol: "BTC", AssetLogo: "", IsActive: true},
		{Guid: "test-asset-usdt", AssetName: "Tether", AssetSymbol: "USDT", AssetLogo: "", IsActive: true},
	}).Error)

	store := NewMarketAggregationDB(tx)
	require.NoError(t, store.UpsertAssetMetrics([]AssetMetricCurrent{
		{
			AssetGuid: "test-asset-btc", Provider: "coingecko", ProviderAssetID: "bitcoin-test",
			MarketCapRank: intPointer(1), MarketCapUSD: textPointer("1300000000000"),
			CirculatingSupply: textPointer("20000000"), ProviderUpdatedAt: &now,
			ObservedAt: now, UpdatedAt: now,
		},
		{
			AssetGuid: "test-asset-usdt", Provider: "coingecko", ProviderAssetID: "tether-test",
			MarketCapRank: intPointer(3), ReferencePriceUSD: textPointer("1"),
			ProviderUpdatedAt: &now, ObservedAt: now, UpdatedAt: now,
		},
	}))
	require.NoError(t, store.UpsertAssetAliases([]AssetAlias{
		{Provider: "coinbase", Alias: "BTC", AssetGuid: "test-asset-btc", ReviewStatus: "approved", CreatedAt: now, UpdatedAt: now},
		{Provider: "coinbase", Alias: "USDT", AssetGuid: "test-asset-usdt", ReviewStatus: "approved", CreatedAt: now, UpdatedAt: now},
	}))
	reviewSource := "integration-fixture"
	require.NoError(t, tx.Create([]AssetRepresentation{
		{
			AssetGuid: "test-asset-btc", ChainID: 1,
			ContractAddress:    "0x00000000000000000000000000000000000000b1",
			RepresentationKind: "wrapped", TokenSymbol: "WBTC", Decimals: 8,
			ReviewStatus: "approved", ReviewSource: &reviewSource, ReviewedAt: &now,
		},
		{
			AssetGuid: "test-asset-usdt", ChainID: 1,
			ContractAddress:    "0x00000000000000000000000000000000000000b2",
			RepresentationKind: "canonical", TokenSymbol: "USDT", Decimals: 6,
			ReviewStatus: "approved", ReviewSource: &reviewSource, ReviewedAt: &now,
		},
	}).Error)
	require.NoError(t, tx.Create(&DexPoolCandidate{
		Provider: "uniswap", ChainID: 1, ProtocolVersion: "v3",
		PoolAddress:   "0x00000000000000000000000000000000000000b3",
		Token0Address: "0x00000000000000000000000000000000000000b1",
		Token1Address: "0x00000000000000000000000000000000000000b2",
		FeeTier:       3000, ResolutionStatus: "resolved", FirstSeenAt: now, LastSeenAt: now,
		RawMetadata: datatypes.JSON([]byte(`{}`)),
	}).Error)
	auditRows, auditCounts, auditTotal, err := store.QueryCatalogAudit("uniswap", "resolved", 50, 1, 50)
	require.NoError(t, err)
	require.EqualValues(t, 1, auditTotal)
	require.Len(t, auditRows, 1)
	require.Equal(t, "dex_pool", auditRows[0].CandidateKind)
	require.NotNil(t, auditRows[0].Rank)
	require.Equal(t, 1, *auditRows[0].Rank)
	require.Equal(t, "approved", auditRows[0].AliasReview)
	require.Len(t, auditCounts, 1)
	require.Equal(t, "resolved", auditCounts[0].Status)
	require.NoError(t, store.UpsertProviderMarketCandidates([]ProviderMarketCandidate{{
		Provider: "coinbase", SourceSymbol: "BTC-USDT", MarketType: "spot",
		BaseAlias: "BTC", QuoteAlias: "USDT", ResolutionStatus: "resolved",
		BaseAssetGuid: textPointer("test-asset-btc"), QuoteAssetGuid: textPointer("test-asset-usdt"),
		FirstSeenAt: now, LastSeenAt: now,
	}}))
	_, err = store.RefreshProviderAssetSelection("coinbase", 1, "integration-test")
	require.NoError(t, err)
	require.NoError(t, store.SetProviderRollout(
		"coinbase", "enabled", 50, nil, nil,
	))
	allowedAssets, rollout, err := store.QueryRolloutAssetIDs("coinbase")
	require.NoError(t, err)
	require.NotNil(t, rollout)
	require.Equal(t, "enabled", rollout.Mode)
	require.Equal(t, map[string]struct{}{"test-asset-btc": {}}, allowedAssets)
	var coinbaseExchange struct {
		Guid string `gorm:"column:guid"`
	}
	require.NoError(t, tx.Table("exchange").Select("guid").Where("code = 'coinbase'").Take(&coinbaseExchange).Error)
	require.NoError(t, tx.Create(&Symbol{
		Guid: "test-legacy-coinbase-symbol", SymbolName: "BTC/USDT",
		BaseAssetGuid: "test-asset-btc", QuoteAssetGuid: "test-asset-usdt",
		MarketType: "spot", IsActive: true,
	}).Error)
	require.NoError(t, tx.Create(&ExchangeSymbol{
		Guid: "test-legacy-coinbase-market", ExchangeGuid: coinbaseExchange.Guid,
		SymbolGuid: "test-legacy-coinbase-symbol", MarketCode: "coinbase:BTC/USDT:spot",
		SourceSymbol: textPointer("BTC-USDT"), Volume: "0", IsActive: true,
	}).Error)

	enabled, err := store.ReconcileResolvedSpotMarkets("coinbase")
	require.NoError(t, err)
	require.EqualValues(t, 1, enabled)

	markets, err := NewExchangeSymbolDB(tx).QueryProviderMarkets("coinbase")
	require.NoError(t, err)
	require.Len(t, markets, 1)
	market := markets[0]
	require.Equal(t, "test-legacy-coinbase-market", market.MarketID)
	klineMarkets, err := NewExchangeSymbolDB(tx).QueryProviderKlineMarkets("coinbase")
	require.NoError(t, err)
	require.Empty(t, klineMarkets)
	require.NoError(t, tx.Table("exchange_symbol").
		Where("guid = ?", market.MarketID).
		Update("kline_enabled", true).Error)
	klineMarkets, err = NewExchangeSymbolDB(tx).QueryProviderKlineMarkets("coinbase")
	require.NoError(t, err)
	require.Len(t, klineMarkets, 1)
	open := "6400000000000"
	change := "1.5625"
	turnover := "1200000000000000000"
	_, err = NewSymbolMarketDB(tx).ApplyMarketSnapshot(MarketSnapshotInput{
		Guid: "test-market-snapshot", MarketID: market.MarketID, SymbolGuid: market.SymbolGuid,
		Price: "6500000000000", AskPrice: "6500100000000", BidPrice: "6499900000000",
		Volume: turnover, Open24h: &open, QuoteTurnover24h: &turnover,
		Change24hPct: &change, IsActive: true, ObservedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, store.UpsertAssetPriceIndexes([]AssetPriceIndex{{
		AssetGuid: "test-asset-btc", PriceUSD: textPointer("65000"),
		Open24hUSD: textPointer("64000"), Change24hPct: textPointer("1.5625"),
		Turnover24hUSD: textPointer("12000000000"), ContributorCount: 1,
		Confidence: "low", Available: true, ObservedAt: now,
	}}))
	require.NoError(t, store.UpsertAssetVenueSnapshots([]AssetVenueSnapshot{{
		AssetGuid: "test-asset-btc", Provider: "all", PriceKind: "composite_spot",
		PriceUSD: textPointer("65000"), Open24hUSD: textPointer("64000"),
		Change24hPct: textPointer("1.5625"), Turnover24hUSD: textPointer("12000000000"),
		ContributorCount: 1, MarketCount: 1, Confidence: "low", Quality: "low",
		Available: true, ObservedAt: now,
	}}))
	var firstVersion int64
	require.NoError(t, tx.Table("asset_price_index").
		Select("version").Where("asset_guid = ?", "test-asset-btc").Scan(&firstVersion).Error)
	require.Greater(t, firstVersion, int64(0))
	require.NoError(t, store.UpsertAssetPriceIndexes([]AssetPriceIndex{{
		AssetGuid: "test-asset-btc", PriceUSD: textPointer("65000"),
		Open24hUSD: textPointer("64000"), Change24hPct: textPointer("1.5625"),
		Turnover24hUSD: textPointer("12000000000"), ContributorCount: 1,
		Confidence: "low", Available: true, ObservedAt: now.Add(5 * time.Second),
	}}))
	var repeatedVersion int64
	require.NoError(t, tx.Table("asset_price_index").
		Select("version").Where("asset_guid = ?", "test-asset-btc").Scan(&repeatedVersion).Error)
	require.Equal(t, firstVersion, repeatedVersion)

	rows, total, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "test-asset-btc", rows[0].AssetID)
	require.EqualValues(t, 1, rows[0].SpotMarketCount)
	coinbaseRows, coinbaseTotal, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 20, Venue: "coinbase",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, coinbaseTotal)
	require.Len(t, coinbaseRows, 1)
	require.EqualValues(t, 1, coinbaseRows[0].SpotMarketCount)
	require.Equal(t, "source_unavailable", coinbaseRows[0].CoverageReason)
	require.Zero(t, coinbaseRows[0].PerpMarketCount)
	require.Zero(t, coinbaseRows[0].DexRouteCount)
	bybitRows, _, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 20, Venue: "bybit", IncludeUncovered: true,
	})
	require.NoError(t, err)
	require.Empty(t, bybitRows)

	assetMarkets, err := store.QueryAssetMarkets("test-asset-btc")
	require.NoError(t, err)
	require.Len(t, assetMarkets, 1)
	require.True(t, equalNumericString("6500000000000", assetMarkets[0].Price))

	later := now.Add(6 * time.Hour)
	require.NoError(t, store.SetProviderRollout("coinbase", "paused", 50, nil, nil))
	pausedRows, _, err := store.QueryAssetIndexDashboard(AssetIndexDashboardQuery{
		Page: 1, PageSize: 20, Venue: "coinbase", IncludeUncovered: true,
	})
	require.NoError(t, err)
	require.Zero(t, pausedRows[0].SpotMarketCount)
	pausedMarkets, err := store.QueryAssetMarkets("test-asset-btc")
	require.NoError(t, err)
	require.Empty(t, pausedMarkets)

	require.NoError(t, store.UpsertProviderMarketCandidates([]ProviderMarketCandidate{{
		Provider: "coinbase", SourceSymbol: "BTC-USDT", MarketType: "spot",
		BaseAlias: "BTC", QuoteAlias: "USDT", ResolutionStatus: "rejected",
		RejectionReason: textPointer("upstream_not_tradable"),
		FirstSeenAt:     now, LastSeenAt: later,
	}}))
	enabled, err = store.EnableResolvedSpotMarkets(
		"coinbase", later, map[string]struct{}{"test-asset-btc": {}},
	)
	require.NoError(t, err)
	require.Zero(t, enabled)
	markets, err = NewExchangeSymbolDB(tx).QueryProviderMarkets("coinbase")
	require.NoError(t, err)
	require.Empty(t, markets)
}

func TestIntegrationProviderObservationStartsAfterRolloutReset(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	statusStore := NewProviderStatusDB(tx)
	rolloutStore := NewMarketAggregationDB(tx)
	first := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Second)
	require.NoError(t, statusStore.RecordAttempt("coinbase", "test-observation-window", first))
	require.NoError(t, statusStore.RecordAttempt("coinbase", "test-observation-window", second))
	status, err := statusStore.QueryProviderStatus("coinbase", "test-observation-window")
	require.NoError(t, err)
	require.NotNil(t, status.ObservationStartedAt)
	require.WithinDuration(t, first, *status.ObservationStartedAt, time.Millisecond)
	require.EqualValues(t, 2, status.AttemptCount)

	require.NoError(t, rolloutStore.SetProviderRollout("coinbase", "paused", 50, nil, nil))
	status, err = statusStore.QueryProviderStatus("coinbase", "test-observation-window")
	require.NoError(t, err)
	require.Nil(t, status.ObservationStartedAt)
	require.Zero(t, status.AttemptCount)

	afterReset := second.Add(time.Hour)
	require.NoError(t, statusStore.RecordAttempt(
		"coinbase", "test-observation-window", afterReset,
	))
	status, err = statusStore.QueryProviderStatus("coinbase", "test-observation-window")
	require.NoError(t, err)
	require.NotNil(t, status.ObservationStartedAt)
	require.WithinDuration(t, afterReset, *status.ObservationStartedAt, time.Millisecond)
	require.EqualValues(t, 1, status.AttemptCount)
}

func TestIntegrationDexCoverageSeparatesQuoteNotional(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)
	tx := gormDB.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()

	assetID := fmt.Sprintf("test-dex-notional-%d", time.Now().UnixNano())
	require.NoError(t, tx.Create(&Asset{
		Guid: assetID, AssetName: "DEX Notional Test",
		AssetSymbol: "DNT", IsActive: true,
	}).Error)
	store := NewMarketAggregationDB(tx)
	start := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.InsertDexQuoteObservations([]DexQuoteObservation{
		{
			Provider: "uniswap", AssetGuid: assetID, RouteKey: "route-a",
			ObservedAt: start.Add(time.Minute), PriceUSD: "100",
			QuoteNotionalUSD: "10000",
		},
		{
			Provider: "uniswap", AssetGuid: assetID, RouteKey: "route-a",
			ObservedAt: start.Add(2 * time.Minute), PriceUSD: "101",
			QuoteNotionalUSD: "1000",
		},
		{
			Provider: "uniswap", AssetGuid: assetID, RouteKey: "route-a",
			ObservedAt: start.Add(3 * time.Minute), PriceUSD: "102",
			QuoteNotionalUSD: "10000",
		},
	}))

	large, err := store.QueryDexWindowCoverage(
		"uniswap", assetID, "route-a", "10000", start, start.Add(time.Hour),
	)
	require.NoError(t, err)
	require.NotNil(t, large)
	require.EqualValues(t, 2, large.ObservationCount)
	require.True(t, equalNumericString("100", large.OpenPriceUSD))

	small, err := store.QueryDexWindowCoverage(
		"uniswap", assetID, "route-a", "1000", start, start.Add(time.Hour),
	)
	require.NoError(t, err)
	require.NotNil(t, small)
	require.EqualValues(t, 1, small.ObservationCount)
	require.True(t, equalNumericString("101", small.OpenPriceUSD))
}

func intPointer(value int) *int { return &value }

func textPointer(value string) *string { return &value }
