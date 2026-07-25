package service

import (
	"context"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/model"
)

const (
	topMoversDefaultLimit int64 = 5
	topMoversMaxLimit     int64 = 20
)

// GetTopMovers 返回 24h 涨幅榜 / 跌幅榜。
// 读路径：优先从 Redis ZSET（market:rank:change24h）按序取 guid，再回表
// symbol_market/symbol/asset 补全展示字段；ZSET 为空或 Redis 不可用时回退为
// 直接按 symbol_market.change_24h_pct 在 SQL 里排序。两个来源都为空时返回空列表
// （code 2000），不返回任何假数据。
func (h HandleSvc) GetTopMovers(request *model.TopMoversRequest) (*model.TopMoversResponse, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = topMoversDefaultLimit
	}
	if limit > topMoversMaxLimit {
		limit = topMoversMaxLimit
	}

	direction := strings.ToLower(strings.TrimSpace(request.Direction))
	if direction != "losers" {
		direction = "gainers"
	}

	source := "redis"
	markets, scores := h.rankMarketsFromRedis(direction, limit)
	if len(markets) == 0 {
		// SQL fallback uses the nullable canonical percentage written by the
		// snapshot writer. Missing values are excluded by the query.
		source = "sql"
		sqlOrder := "desc"
		if direction == "losers" {
			sqlOrder = "asc"
		}
		list, err := h.symbolMarketView.QuerySymbolMarketsByChange(sqlOrder, limit)
		if err != nil {
			log.Error("query top movers fallback sql error", "error", err)
			return nil, err
		}
		markets = list
		scores = map[string]float64{}
	}

	items, err := h.buildTopMoverItems(markets, scores)
	if err != nil {
		return nil, err
	}

	return &model.TopMoversResponse{
		Code:      2000,
		Message:   "get top movers success",
		Result:    items,
		Total:     int64(len(items)),
		Direction: direction,
		Source:    source,
	}, nil
}

// rankMarketsFromRedis 从 ZSET 按序取 symbol_guid，再按 guid 回表并保持榜单顺序。
// Redis 不可用 / 榜单为空时返回 nil，由调用方走 SQL 回退。
func (h HandleSvc) rankMarketsFromRedis(direction string, limit int64) ([]*database.SymbolMarket, map[string]float64) {
	if h.redisCli == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	card, err := h.redisCli.ZCard(ctx, marketkey.RankChange24hKey)
	if err != nil {
		log.Error("top movers zcard failed, fallback to sql", "error", err)
		return nil, nil
	}
	if card == 0 {
		return nil, nil
	}

	var pairs []redis.ZScorePair
	if direction == "losers" {
		pairs, err = h.redisCli.ZRangeWithScores(ctx, marketkey.RankChange24hKey, 0, limit-1)
	} else {
		pairs, err = h.redisCli.ZRevRangeWithScores(ctx, marketkey.RankChange24hKey, 0, limit-1)
	}
	if err != nil {
		log.Error("top movers zrange failed, fallback to sql", "error", err)
		return nil, nil
	}
	if len(pairs) == 0 {
		return nil, nil
	}

	guids := make([]string, 0, len(pairs))
	scores := make(map[string]float64, len(pairs))
	for _, p := range pairs {
		guids = append(guids, p.Member)
		scores[p.Member] = p.Score
	}

	list, err := h.symbolMarketView.QuerySymbolMarketsByGuids(guids)
	if err != nil {
		log.Error("query symbol_market by rank guids error", "error", err)
		return nil, nil
	}
	byGuid := make(map[string]*database.SymbolMarket, len(list))
	for _, m := range list {
		byGuid[m.SymbolGuid] = m
	}
	// 按 ZSET 顺序重组；ZSET 有但 DB 无行情的 guid 直接跳过
	ordered := make([]*database.SymbolMarket, 0, len(guids))
	for _, guid := range guids {
		if m, ok := byGuid[guid]; ok {
			ordered = append(ordered, m)
		}
	}
	return ordered, scores
}

// buildTopMoverItems 把行情行转成榜单条目：数值还原 + 关联 symbol/asset 补
// symbol_name、资产名和 logo，与 get_market_dashboard 的条目字段保持一致。
func (h HandleSvc) buildTopMoverItems(markets []*database.SymbolMarket, scores map[string]float64) ([]model.TopMoverItem, error) {
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
	marketIDs := make([]string, 0, len(markets))
	for _, market := range markets {
		marketIDs = append(marketIDs, market.MarketID)
	}
	metadata, err := h.exchangeSymbolView.QueryMarketMetadataByIDs(marketIDs)
	if err != nil {
		return nil, err
	}

	items := make([]model.TopMoverItem, 0, len(markets))
	for i, m := range markets {
		item := model.TopMoverItem{
			Rank:             int64(i + 1),
			MarketID:         m.MarketID,
			Symbol:           m.SymbolGuid,
			Price:            unscaleString(m.Price, 8),
			Volume:           unscaleString(m.Volume, 8),
			MarketCap:        unscaleString(m.MarketCap, 8),
			UpdatedAt:        m.UpdatedAt.UnixMilli(),
			DataDelaySeconds: marketDataDelaySeconds(m.UpdatedAt),
		}
		if meta, ok := metadata[m.MarketID]; ok {
			item.MarketCode = meta.MarketCode
			item.Exchange = meta.Exchange
			item.MarketType = meta.MarketType
		}
		if change, ok, _ := canonicalChange(scores, m.SymbolGuid, m.Change24hPct); ok {
			item.Change24h = change
			item.ChangeAvailable = true
		}
		if sym, ok := symbolMap[m.SymbolGuid]; ok {
			item.Symbol = sym.SymbolName
			if asset, ok := assetMap[sym.BaseAssetGuid]; ok {
				item.Name = asset.AssetName
				item.Logo = asset.AssetLogo
			}
		}
		items = append(items, item)
	}
	return items, nil
}
