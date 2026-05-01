package service

import (
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

type RestService interface {
	GetSupportAssets(*model.SupportAssetRequest) (*model.SupportAssetResponse, error)
	GetMarketDashboard(*model.MarketDashboardRequest) (*model.MarketDashboardResponse, error)
	GetExchanges(*model.CommonRequest) (*model.ExchangeResponse, error)
	GetSymbols(*model.CommonRequest) (*model.SymbolResponse, error)
	GetSystemOverview(*model.CommonRequest) (*model.SystemOverviewResponse, error)
	GetKlines(*model.KlinesRequest) (*model.KlinesResponse, error)
	GetFiatRates(*model.FiatRatesRequest) (*model.FiatRatesResponse, error)
}

type HandleSvc struct {
	assetView        database.AssetView
	symbolView       database.SymbolView
	symbolMarketView database.SymbolMarketView
	exchangeView     database.ExchangeView
	symbolKlineView  database.SymbolKlineView
}

func NewHandleSvc(assetView database.AssetView, symbolView database.SymbolView, symbolMarketView database.SymbolMarketView, exchangeView database.ExchangeView, symbolKlineView database.SymbolKlineView) RestService {
	return &HandleSvc{
		assetView:        assetView,
		symbolView:       symbolView,
		symbolMarketView: symbolMarketView,
		exchangeView:     exchangeView,
		symbolKlineView:  symbolKlineView,
	}
}
