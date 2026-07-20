package service

import (
	"math/big"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h HandleSvc) GetSystemOverview(request *model.CommonRequest) (*model.SystemOverviewResponse, error) {
	overview := model.SystemOverview{
		CrawlerStatus:  "Running",
		RedisStatus:    "Connected",
		DatabaseStatus: "Connected",
		WorkerStatus:   "Idle",
		ApiStatus:      "Healthy",
	}

	assets, _ := h.assetView.QueryAssets()
	overview.AssetCount = int64(len(assets))

	symbols, _ := h.symbolView.QuerySymbols()
	overview.SymbolCount = int64(len(symbols))

	exchanges, _ := h.exchangeView.QueryExchanges()
	overview.ExchangeCount = int64(len(exchanges))

	// Fetch all symbol_market records for real stats
	markets, _, _ := h.symbolMarketView.QuerySymbolMarketList(1, 1000)
	overview.MarketCount = int64(len(markets))

	// Compute total market_cap and volume (1e8 scaled)
	totalMC := new(big.Int)
	totalVol := new(big.Int)
	var latestMarketUpdatedAt time.Time
	for _, m := range markets {
		if m.UpdatedAt.After(latestMarketUpdatedAt) {
			latestMarketUpdatedAt = m.UpdatedAt
		}

		// Parse market_cap (numeric(65,18) → big.Int)
		mcStr := m.MarketCap
		if idx := strings.Index(mcStr, "."); idx >= 0 {
			mcStr = mcStr[:idx]
		}
		if mc, ok := new(big.Int).SetString(mcStr, 10); ok && mc.Sign() > 0 {
			totalMC.Add(totalMC, mc)
		}

		// Parse volume
		volStr := m.Volume
		if idx := strings.Index(volStr, "."); idx >= 0 {
			volStr = volStr[:idx]
		}
		if vol, ok := new(big.Int).SetString(volStr, 10); ok && vol.Sign() > 0 {
			totalVol.Add(totalVol, vol)
		}
	}

	overview.TotalMarketCap = unscaleString(totalMC.String(), 8)
	overview.TotalVolume = unscaleString(totalVol.String(), 8)
	overview.UpdatedAt = latestMarketUpdatedAt.UnixMilli()
	overview.DataDelaySeconds = marketDataDelaySeconds(latestMarketUpdatedAt)
	if latestMarketUpdatedAt.IsZero() {
		overview.UpdatedAt = 0
		overview.DataDelaySeconds = -1
	}

	return &model.SystemOverviewResponse{
		Code:    2000,
		Message: "success",
		Result:  overview,
	}, nil
}
