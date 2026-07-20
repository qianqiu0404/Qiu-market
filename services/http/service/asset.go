package service

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/database"
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
	marketList, total, err := h.symbolMarketView.QuerySymbolMarketList(request.Page, request.PageSize)
	if err != nil {
		log.Error("query symbol market list error", "error", err)
		return nil, err
	}

	symbols, err := h.symbolView.QuerySymbols()
	if err != nil {
		log.Error("query symbols error", "error", err)
		return nil, err
	}

	symbolMap := make(map[string]interface{}) // Quick fix for lookup
	for _, s := range symbols {
		symbolMap[s.Guid] = s
	}

	assets, err := h.assetView.QueryAssets()
	if err != nil {
		log.Error("query assets error", "error", err)
		return nil, err
	}
	assetMap := make(map[string]interface{})
	for _, a := range assets {
		assetMap[a.Guid] = a
	}

	var result []model.MarketDashboardItem
	for _, m := range marketList {
		// 先处理数值还原
		price := unscaleString(m.Price, 8)
		volume := unscaleString(m.Volume, 8)
		marketCap := unscaleString(m.MarketCap, 8)

		item := model.MarketDashboardItem{
			Symbol:           m.SymbolGuid,
			Price:            price,
			Volume:           volume,
			Change24h:        m.Radio,
			MarketCap:        marketCap,
			UpdatedAt:        m.UpdatedAt.UnixMilli(),
			DataDelaySeconds: marketDataDelaySeconds(m.UpdatedAt),
		}

		if s, ok := symbolMap[m.SymbolGuid]; ok {
			if sym, ok := s.(*database.Symbol); ok {
				item.Symbol = sym.SymbolName
				if a, ok := assetMap[sym.BaseAssetGuid]; ok {
					if asset, ok := a.(*database.Asset); ok {
						item.Name = asset.AssetName
						item.Logo = asset.AssetLogo
					}
				}
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
	var res []model.Symbol
	for _, s := range list {
		res = append(res, model.Symbol{
			Guid:         s.Guid,
			BaseAsset:    s.BaseAssetGuid,
			QuoteAsset:   s.QuoteAssetGuid,
			SymbolName:   s.SymbolName,
			BaseAssetId:  s.BaseAssetGuid,
			QuoteAssetId: s.QuoteAssetGuid,
		})
	}
	return &model.SymbolResponse{
		Code:    2000,
		Message: "success",
		Result:  res,
		Total:   int64(len(res)),
	}, nil
}
