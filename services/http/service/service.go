package service

import (
	"database/sql"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/model"
)

type RestService interface {
	GetSupportAssets(*model.SupportAssetRequest) (*model.SupportAssetResponse, error)
	GetMarketDashboard(*model.MarketDashboardRequest) (*model.MarketDashboardResponse, error)
	GetAssetDashboard(*model.AssetDashboardRequest) (*model.AssetDashboardResponse, error)
	GetMarketInsights(*model.MarketInsightsRequest) (*model.MarketInsightsResponse, error)
	GetExchanges(*model.CommonRequest) (*model.ExchangeResponse, error)
	GetSymbols(*model.CommonRequest) (*model.SymbolResponse, error)
	GetSystemOverview(*model.CommonRequest) (*model.SystemOverviewResponse, error)
	GetKlines(*model.KlinesRequest) (*model.KlinesResponse, error)
	GetMarketSparklines(*model.MarketSparklinesRequest) (*model.MarketSparklinesResponse, error)
	GetFiatRates(*model.FiatRatesRequest) (*model.FiatRatesResponse, error)
	GetTopMovers(*model.TopMoversRequest) (*model.TopMoversResponse, error)
	GetKlineAnalytics(*model.KlineAnalyticsRequest) (*model.KlineAnalyticsResponse, error)
	GetAssetMomentum(*model.AssetMomentumRequest) (*model.AssetMomentumResponse, error)
	GetMarketOverview(*model.MarketOverviewRequest) (*model.MarketOverviewResponse, error)
	GetAssetDashboardV2(*model.AssetDashboardV2Request) (*model.AssetDashboardV2Response, error)
	GetMarketPriceTicks(*model.MarketPriceTicksRequest) (*model.MarketPriceTicksResponse, error)
	GetAssetMarkets(*model.AssetMarketsRequest) (*model.AssetMarketsResponse, error)
	GetAssetVenues(*model.AssetVenuesRequest) (*model.AssetVenuesResponse, error)
	GetProviderCatalogAudit(*model.ProviderCatalogAuditRequest) (*model.ProviderCatalogAuditResponse, error)
}

type HandleSvc struct {
	db                    *database.DB
	assetView             database.AssetView
	symbolView            database.SymbolView
	symbolMarketView      database.SymbolMarketView
	exchangeView          database.ExchangeView
	exchangeSymbolView    database.ExchangeSymbolView
	symbolKlineView       database.SymbolKlineView
	providerStatusView    database.ProviderStatusDB
	marketAggregationView database.MarketAggregationDB
	marketSnapshots       *marketSnapshotStore
	// redisCli 用于读取 ZSET 榜单；为 nil 时榜单接口自动回退 SQL 排序。
	redisCli *redis.Client
	// dorisDB 是 Doris 数仓的只读连接（MySQL 协议）；为 nil 时
	// get_kline_analytics 返回 ErrDorisUnavailable（显式报错，不回退 PG）。
	dorisDB *sql.DB
}

func NewHandleSvc(db *database.DB, assetView database.AssetView, symbolView database.SymbolView, symbolMarketView database.SymbolMarketView, exchangeView database.ExchangeView, exchangeSymbolView database.ExchangeSymbolView, symbolKlineView database.SymbolKlineView, providerStatusView database.ProviderStatusDB, marketAggregationView database.MarketAggregationDB, redisCli *redis.Client, dorisDB *sql.DB, snapshotContracts ...MarketSnapshotContract) RestService {
	var snapshotContract MarketSnapshotContract
	if len(snapshotContracts) > 0 {
		snapshotContract = snapshotContracts[0]
	}
	return &HandleSvc{
		db:                    db,
		assetView:             assetView,
		symbolView:            symbolView,
		symbolMarketView:      symbolMarketView,
		exchangeView:          exchangeView,
		exchangeSymbolView:    exchangeSymbolView,
		symbolKlineView:       symbolKlineView,
		providerStatusView:    providerStatusView,
		marketAggregationView: marketAggregationView,
		marketSnapshots:       newMarketSnapshotStore(marketAggregationView, redisCli, snapshotContract),
		redisCli:              redisCli,
		dorisDB:               dorisDB,
	}
}
