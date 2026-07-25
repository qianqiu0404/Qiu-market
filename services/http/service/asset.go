package service

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h HandleSvc) GetSupportAssets(request *model.SupportAssetRequest) (*model.SupportAssetResponse, error) {
	assetList, err := h.assetView.QueryAssets()
	if err != nil {
		log.Error("query assets error", "error", err)
		return nil, err
	}
	var supportAssetList []model.SupportAsset
	for _, asset := range assetList {
		supportAsset := model.SupportAsset{
			Guid:        asset.Guid,
			AssetName:   asset.AssetName,
			AssetSymbol: asset.AssetSymbol,
			AssetLogo:   asset.AssetLogo,
		}
		supportAssetList = append(supportAssetList, supportAsset)
	}
	return &model.SupportAssetResponse{
		Code:    2000,
		Message: "get support asset success",
		Result:  supportAssetList,
	}, nil
}

func (h HandleSvc) GetMarketDashboard(request *model.MarketDashboardRequest) (*model.MarketDashboardResponse, error) {
	rankOrder := []string(nil)
	if strings.EqualFold(strings.TrimSpace(request.SortBy), "change24h") {
		rankOrder = h.changeRankOrder(request.SortDirection)
	}
	marketList, total, err := h.symbolMarketView.QuerySymbolMarketList(database.SymbolMarketListQuery{
		Page:          request.Page,
		PageSize:      request.PageSize,
		Exchange:      strings.TrimSpace(request.Exchange),
		Search:        strings.TrimSpace(request.Search),
		MarketID:      strings.TrimSpace(request.MarketID),
		SortBy:        request.SortBy,
		SortDirection: request.SortDirection,
		RankOrder:     rankOrder,
	})
	if err != nil {
		log.Error("query symbol market list error", "error", err)
		return nil, err
	}

	symbols, err := h.symbolView.QuerySymbols()
	if err != nil {
		log.Error("query symbols error", "error", err)
		return nil, err
	}

	symbolMap := make(map[string]*database.Symbol, len(symbols))
	for _, s := range symbols {
		symbolMap[s.Guid] = s
	}

	assets, err := h.assetView.QueryAssets()
	if err != nil {
		log.Error("query assets error", "error", err)
		return nil, err
	}
	assetMap := make(map[string]*database.Asset, len(assets))
	for _, a := range assets {
		assetMap[a.Guid] = a
	}

	marketIDs := make([]string, 0, len(marketList))
	symbolGuids := make([]string, 0, len(marketList))
	for _, m := range marketList {
		marketIDs = append(marketIDs, m.MarketID)
		symbolGuids = append(symbolGuids, m.SymbolGuid)
	}
	metadata, err := h.exchangeSymbolView.QueryMarketMetadataByIDs(marketIDs)
	if err != nil {
		return nil, err
	}
	klineAvailability, err := h.symbolKlineView.QueryMarketKlineAvailability(marketIDs)
	if err != nil {
		return nil, err
	}
	changeScores := h.rankScores(symbolGuids)

	var result []model.MarketDashboardItem
	for _, m := range marketList {
		meta := metadata[m.MarketID]
		item := model.MarketDashboardItem{
			MarketID:         m.MarketID,
			MarketCode:       meta.MarketCode,
			Symbol:           m.SymbolGuid,
			Price:            unscaleString(m.Price, 8),
			Volume:           unscaleString(m.Volume, 8),
			MarketCap:        unscaleString(m.MarketCap, 8),
			Exchange:         meta.Exchange,
			MarketType:       meta.MarketType,
			HasKline:         klineAvailability[m.MarketID],
			ChangeSource:     "unavailable",
			UpdatedAt:        m.UpdatedAt.UnixMilli(),
			DataDelaySeconds: marketDataDelaySeconds(m.UpdatedAt),
		}
		item.ProviderUpdatedAt, item.FreshnessStatus = marketFreshness(meta.Exchange, m.ObservedAt)

		if change, ok, source := canonicalChange(changeScores, m.SymbolGuid, m.Change24hPct); ok {
			item.Change24h = change
			item.ChangeAvailable = true
			item.ChangeSource = source
		}
		if sym, ok := symbolMap[m.SymbolGuid]; ok {
			item.Symbol = sym.SymbolName
			item.BaseAssetID = sym.BaseAssetGuid
			item.QuoteAssetID = sym.QuoteAssetGuid
			if asset, ok := assetMap[sym.BaseAssetGuid]; ok {
				item.Name = asset.AssetName
				item.Logo = asset.AssetLogo
				item.BaseAsset = asset.AssetSymbol
			}
			if asset, ok := assetMap[sym.QuoteAssetGuid]; ok {
				item.QuoteAsset = asset.AssetSymbol
			}
		}
		result = append(result, item)
	}

	return &model.MarketDashboardResponse{
		Code:    2000,
		Message: "get market dashboard success",
		Result:  result,
		Total:   total,
	}, nil
}

func (h HandleSvc) changeRankOrder(direction string) []string {
	if h.redisCli == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var (
		pairs []redis.ZScorePair
		err   error
	)
	if strings.EqualFold(direction, "asc") {
		pairs, err = h.redisCli.ZRangeWithScores(ctx, marketkey.RankChange24hKey, 0, -1)
	} else {
		pairs, err = h.redisCli.ZRevRangeWithScores(ctx, marketkey.RankChange24hKey, 0, -1)
	}
	if err != nil {
		log.Warn("query global change rank failed", "error", err)
		return nil
	}
	order := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		order = append(order, pair.Member)
	}
	return order
}

func (h HandleSvc) rankScores(symbolGuids []string) map[string]float64 {
	if h.redisCli == nil || len(symbolGuids) == 0 {
		return map[string]float64{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	scores, err := h.redisCli.ZScores(ctx, marketkey.RankChange24hKey, symbolGuids)
	if err != nil {
		log.Warn("query market change scores failed", "error", err)
		return map[string]float64{}
	}
	return scores
}

func marketDataDelaySeconds(updatedAt time.Time) int64 {
	if updatedAt.IsZero() {
		return -1
	}
	delay := time.Since(updatedAt)
	if delay < 0 {
		return 0
	}
	return int64(delay.Seconds())
}

func unscaleString(valStr string, decimals int) string {
	if valStr == "" || valStr == "0" {
		return "0"
	}

	if strings.Contains(valStr, ".") {
		// 带小数点：判断小数部分是否全 0
		parts := strings.Split(valStr, ".")
		if len(parts) == 2 {
			decimalPart := strings.TrimRight(parts[1], "0")
			if decimalPart == "" {
				// 小数部分全 0 → 是 crawler 写入的整数（如 "137660000.000000"）→ /1e8
				return unscaleBigIntString(parts[0], decimals)
			}
			// 小数部分有非 0 值 → seed 原始小数 → 清理多余 0 后返回
			out := strings.TrimRight(valStr, "0")
			out = strings.TrimRight(out, ".")
			if out == "" {
				return "0"
			}
			return out
		}
	}

	// 无小数点 → crawler 写入的 1e8 放大整数 → 除以 1e8 还原
	return unscaleBigIntString(valStr, decimals)
}

func unscaleBigIntString(valStr string, decimals int) string {
	bi, ok := new(big.Int).SetString(valStr, 10)
	if !ok {
		return valStr
	}

	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)

	bf := new(big.Float).SetInt(bi)
	bm := new(big.Float).SetInt(multiplier)

	res := new(big.Float).Quo(bf, bm)

	out := fmt.Sprintf("%.8f", res)
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if out == "" {
		return "0"
	}
	return out
}

func (h HandleSvc) GetExchanges(request *model.CommonRequest) (*model.ExchangeResponse, error) {
	list, err := h.exchangeView.QueryExchanges()
	if err != nil {
		return nil, err
	}
	var res []model.Exchange
	for _, e := range list {
		res = append(res, model.Exchange{
			Guid: e.Guid,
			Name: e.Name,
			Logo: "",
		})
	}
	return &model.ExchangeResponse{
		Code:    2000,
		Message: "success",
		Result:  res,
		Total:   int64(len(res)),
	}, nil
}

func (h HandleSvc) GetSymbols(request *model.CommonRequest) (*model.SymbolResponse, error) {
	list, err := h.symbolView.QuerySymbols()
	if err != nil {
		return nil, err
	}
	// 批量解析交易所名，前端 Klines 页按交易所分组展示交易对
	guids := make([]string, 0, len(list))
	for _, s := range list {
		guids = append(guids, s.Guid)
	}
	exchangeNames, err := h.exchangeSymbolView.QueryExchangeNamesBySymbolGuids(guids)
	if err != nil {
		log.Error("query exchange names for symbols error", "error", err)
		exchangeNames = map[string]string{}
	}
	assets, err := h.assetView.QueryAssets()
	if err != nil {
		return nil, err
	}
	assetSymbols := make(map[string]string, len(assets))
	for _, asset := range assets {
		assetSymbols[asset.Guid] = asset.AssetSymbol
	}
	var res []model.Symbol
	for _, s := range list {
		res = append(res, model.Symbol{
			Guid:         s.Guid,
			BaseAsset:    assetSymbols[s.BaseAssetGuid],
			QuoteAsset:   assetSymbols[s.QuoteAssetGuid],
			SymbolName:   s.SymbolName,
			BaseAssetId:  s.BaseAssetGuid,
			QuoteAssetId: s.QuoteAssetGuid,
			Exchange:     exchangeNames[s.Guid],
			MarketType:   s.MarketType,
		})
	}
	return &model.SymbolResponse{
		Code:    2000,
		Message: "success",
		Result:  res,
		Total:   int64(len(res)),
	}, nil
}
