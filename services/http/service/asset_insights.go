package service

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

const crossVenueFreshnessLimitSeconds int64 = 60

type assetReadModel struct {
	item model.AssetDashboardItem
}

type marketCandidate struct {
	item       model.AssetMarketItem
	symbolGuid string
}

func (h HandleSvc) GetAssetDashboard(request *model.AssetDashboardRequest) (*model.AssetDashboardResponse, error) {
	assets, err := h.loadAssetReadModels(strings.TrimSpace(request.Search))
	if err != nil {
		return nil, err
	}
	sortAssetReadModels(assets, request.SortBy, request.SortDirection)

	page := request.Page
	if page < 1 {
		page = 1
	}
	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start > int64(len(assets)) {
		start = int64(len(assets))
	}
	end := start + pageSize
	if end > int64(len(assets)) {
		end = int64(len(assets))
	}
	result := make([]model.AssetDashboardItem, 0, end-start)
	for _, asset := range assets[start:end] {
		result = append(result, asset.item)
	}
	return &model.AssetDashboardResponse{
		Code:    2000,
		Message: "get asset dashboard success",
		Result:  result,
		Total:   int64(len(assets)),
	}, nil
}

func (h HandleSvc) GetMarketInsights(_ *model.MarketInsightsRequest) (*model.MarketInsightsResponse, error) {
	assets, err := h.loadAssetReadModels("")
	if err != nil {
		return nil, err
	}
	breadth := model.MarketBreadth{AssetCount: int64(len(assets))}
	knownChanges := make([]float64, 0, len(assets))
	totalTurnover := new(big.Rat)
	for _, asset := range assets {
		totalTurnover.Add(totalTurnover, decimalRat(asset.item.Turnover24h))
		if !asset.item.ChangeAvailable {
			breadth.Unknown++
			continue
		}
		change, err := strconv.ParseFloat(asset.item.Change24h, 64)
		if err != nil {
			breadth.Unknown++
			continue
		}
		knownChanges = append(knownChanges, change)
		switch {
		case change > 0:
			breadth.Advancers++
		case change < 0:
			breadth.Decliners++
		default:
			breadth.Flat++
		}
	}
	knownCount := breadth.Advancers + breadth.Decliners + breadth.Flat
	if knownCount > 0 {
		breadth.AdvanceRatio = decimalFloat(float64(breadth.Advancers)/float64(knownCount)*100, 4)
	}
	breadth.MedianChange24h = decimalFloat(median(knownChanges), 4)
	breadth.Turnover24h = ratDecimal(totalTurnover, 8)

	return &model.MarketInsightsResponse{
		Code:    2000,
		Message: "get market insights success",
		Result: model.MarketInsightsResult{
			Breadth:      breadth,
			Distribution: buildChangeDistribution(knownChanges),
			CrossVenue:   buildCrossVenueItems(assets),
			UpdatedAt:    time.Now().UnixMilli(),
		},
	}, nil
}

func (h HandleSvc) loadAssetReadModels(search string) ([]assetReadModel, error) {
	rows, err := h.symbolMarketView.QueryMarketReadRows(search)
	if err != nil {
		log.Error("query asset market read rows failed", "error", err)
		return nil, err
	}
	marketIDs := make([]string, 0, len(rows))
	symbolGuids := make([]string, 0, len(rows))
	for _, row := range rows {
		marketIDs = append(marketIDs, row.MarketID)
		symbolGuids = append(symbolGuids, row.SymbolGuid)
	}
	availability, err := h.symbolKlineView.QueryMarketKlineAvailability(marketIDs)
	if err != nil {
		return nil, err
	}
	scores := h.rankScores(symbolGuids)
	return aggregateAssetRows(rows, scores, availability), nil
}

func aggregateAssetRows(
	rows []database.MarketReadRow,
	scores map[string]float64,
	availability map[string]bool,
) []assetReadModel {
	type group struct {
		assetID     string
		assetSymbol string
		assetName   string
		logo        string
		markets     []marketCandidate
	}
	groups := make(map[string]*group)
	order := make([]string, 0)
	for _, row := range rows {
		if row.BaseAssetID == "" {
			continue
		}
		g, ok := groups[row.BaseAssetID]
		if !ok {
			g = &group{
				assetID:     row.BaseAssetID,
				assetSymbol: row.BaseAsset,
				assetName:   row.BaseAssetName,
				logo:        row.BaseAssetLogo,
			}
			groups[row.BaseAssetID] = g
			order = append(order, row.BaseAssetID)
		}
		item := model.AssetMarketItem{
			MarketID:         row.MarketID,
			MarketCode:       row.MarketCode,
			Symbol:           row.SymbolName,
			Exchange:         row.Exchange,
			MarketType:       strings.ToLower(row.MarketType),
			QuoteAssetID:     row.QuoteAssetID,
			QuoteAsset:       row.QuoteAsset,
			Price:            unscaleString(row.Price, 8),
			Volume:           unscaleString(row.Volume, 8),
			MarketCap:        unscaleString(row.MarketCap, 8),
			HasKline:         availability[row.MarketID],
			UpdatedAt:        row.UpdatedAt.UnixMilli(),
			DataDelaySeconds: marketDataDelaySeconds(row.UpdatedAt),
		}
		item.ProviderUpdatedAt, item.FreshnessStatus = marketFreshness(row.Exchange, row.ObservedAt)
		if change, ok, _ := canonicalChange(scores, row.SymbolGuid, row.Change24hPct); ok {
			item.Change24h = change
			item.ChangeAvailable = true
		}
		g.markets = append(g.markets, marketCandidate{item: item, symbolGuid: row.SymbolGuid})
	}

	result := make([]assetReadModel, 0, len(groups))
	for _, assetID := range order {
		g := groups[assetID]
		if len(g.markets) == 0 {
			continue
		}
		refIndex := referenceMarketIndex(g.markets)
		maxCap := new(big.Rat)
		turnover := new(big.Rat)
		for i := range g.markets {
			market := &g.markets[i].item
			capValue := decimalRat(market.MarketCap)
			if capValue.Sign() > 0 && capValue.Cmp(maxCap) > 0 {
				maxCap.Set(capValue)
			}
			turnover.Add(turnover, decimalRat(market.Volume))
			market.IsReference = i == refIndex
		}
		ref := g.markets[refIndex].item
		sort.SliceStable(g.markets, func(i, j int) bool {
			left, right := g.markets[i].item, g.markets[j].item
			if left.IsReference != right.IsReference {
				return left.IsReference
			}
			if left.Exchange != right.Exchange {
				return left.Exchange < right.Exchange
			}
			if left.MarketType != right.MarketType {
				return left.MarketType < right.MarketType
			}
			return left.MarketID < right.MarketID
		})
		markets := make([]model.AssetMarketItem, 0, len(g.markets))
		for _, candidate := range g.markets {
			markets = append(markets, candidate.item)
		}
		result = append(result, assetReadModel{
			item: model.AssetDashboardItem{
				AssetID:             g.assetID,
				AssetSymbol:         g.assetSymbol,
				AssetName:           g.assetName,
				Logo:                g.logo,
				ReferenceMarketID:   ref.MarketID,
				ReferenceMarketCode: ref.MarketCode,
				ReferenceExchange:   ref.Exchange,
				ReferenceMarketType: ref.MarketType,
				Price:               ref.Price,
				Change24h:           ref.Change24h,
				ChangeAvailable:     ref.ChangeAvailable,
				MarketCap:           ratDecimal(maxCap, 8),
				Turnover24h:         ratDecimal(turnover, 8),
				MarketCount:         int64(len(markets)),
				HasKline:            ref.HasKline,
				UpdatedAt:           ref.UpdatedAt,
				DataDelaySeconds:    ref.DataDelaySeconds,
				Markets:             markets,
				ProviderUpdatedAt:   ref.ProviderUpdatedAt,
				FreshnessStatus:     ref.FreshnessStatus,
			},
		})
	}
	return result
}

func marketFreshness(exchange string, observedAt *time.Time) (int64, string) {
	if observedAt == nil || observedAt.IsZero() {
		return 0, "Unknown"
	}
	delay := time.Since(observedAt.UTC())
	if delay < 0 {
		delay = 0
	}
	healthyLimit := 2 * time.Minute
	unavailableLimit := 5 * time.Minute
	if strings.EqualFold(exchange, "Hyperliquid") {
		healthyLimit = 30 * time.Second
		unavailableLimit = 90 * time.Second
	}
	switch {
	case delay <= healthyLimit:
		return observedAt.UnixMilli(), "Healthy"
	case delay <= unavailableLimit:
		return observedAt.UnixMilli(), "Stale"
	default:
		return observedAt.UnixMilli(), "Unavailable"
	}
}

func referenceMarketIndex(markets []marketCandidate) int {
	best := -1
	// A positive-cap spot is the strongest reference candidate.
	for i, market := range markets {
		if !strings.EqualFold(market.item.MarketType, "spot") || decimalRat(market.item.MarketCap).Sign() <= 0 {
			continue
		}
		if best == -1 || betterReference(market.item, markets[best].item, true) {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	// A spot without market cap still preserves spot-preferred reference pricing.
	for i, market := range markets {
		if !strings.EqualFold(market.item.MarketType, "spot") {
			continue
		}
		if best == -1 || betterReference(market.item, markets[best].item, false) {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	for i, market := range markets {
		if best == -1 || betterReference(market.item, markets[best].item, false) {
			best = i
		}
	}
	return best
}

func betterReference(left, right model.AssetMarketItem, compareCap bool) bool {
	if compareCap {
		if cmp := decimalRat(left.MarketCap).Cmp(decimalRat(right.MarketCap)); cmp != 0 {
			return cmp > 0
		}
	}
	if cmp := decimalRat(left.Volume).Cmp(decimalRat(right.Volume)); cmp != 0 {
		return cmp > 0
	}
	return left.MarketID < right.MarketID
}

func sortAssetReadModels(assets []assetReadModel, sortBy, direction string) {
	ascending := strings.EqualFold(direction, "asc")
	sort.SliceStable(assets, func(i, j int) bool {
		left, right := assets[i].item, assets[j].item
		var cmp int
		switch strings.ToLower(strings.TrimSpace(sortBy)) {
		case "symbol":
			cmp = strings.Compare(left.AssetSymbol, right.AssetSymbol)
		case "price":
			cmp = decimalRat(left.Price).Cmp(decimalRat(right.Price))
		case "volume", "turnover24h":
			cmp = decimalRat(left.Turnover24h).Cmp(decimalRat(right.Turnover24h))
		case "change24h":
			if left.ChangeAvailable != right.ChangeAvailable {
				return left.ChangeAvailable
			}
			cmp = decimalRat(left.Change24h).Cmp(decimalRat(right.Change24h))
		case "market_cap":
			cmp = decimalRat(left.MarketCap).Cmp(decimalRat(right.MarketCap))
			if cmp == 0 {
				cmp = decimalRat(left.Turnover24h).Cmp(decimalRat(right.Turnover24h))
			}
		default:
			leftPositive := decimalRat(left.MarketCap).Sign() > 0
			rightPositive := decimalRat(right.MarketCap).Sign() > 0
			if leftPositive != rightPositive {
				return leftPositive
			}
			if cmp = decimalRat(left.MarketCap).Cmp(decimalRat(right.MarketCap)); cmp == 0 {
				cmp = decimalRat(left.Turnover24h).Cmp(decimalRat(right.Turnover24h))
			}
			if cmp == 0 {
				return left.AssetID < right.AssetID
			}
			return cmp > 0
		}
		if cmp == 0 {
			return left.AssetID < right.AssetID
		}
		if ascending {
			return cmp < 0
		}
		return cmp > 0
	})
}

func buildChangeDistribution(values []float64) []model.ChangeDistributionBucket {
	buckets := []model.ChangeDistributionBucket{
		{Key: "lt_-10", Label: "< -10%", Max: "-10"},
		{Key: "-10_-5", Label: "-10% to -5%", Min: "-10", Max: "-5"},
		{Key: "-5_-2", Label: "-5% to -2%", Min: "-5", Max: "-2"},
		{Key: "-2_0", Label: "-2% to 0%", Min: "-2", Max: "0"},
		{Key: "0_2", Label: "0% to 2%", Min: "0", Max: "2"},
		{Key: "2_5", Label: "2% to 5%", Min: "2", Max: "5"},
		{Key: "5_10", Label: "5% to 10%", Min: "5", Max: "10"},
		{Key: "gte_10", Label: "≥ 10%", Min: "10"},
	}
	for _, value := range values {
		index := 0
		switch {
		case value < -10:
			index = 0
		case value < -5:
			index = 1
		case value < -2:
			index = 2
		case value < 0:
			index = 3
		case value < 2:
			index = 4
		case value < 5:
			index = 5
		case value < 10:
			index = 6
		default:
			index = 7
		}
		buckets[index].Count++
	}
	return buckets
}

func buildCrossVenueItems(assets []assetReadModel) []model.CrossVenueItem {
	result := make([]model.CrossVenueItem, 0)
	for _, asset := range assets {
		var spot, perp *model.AssetMarketItem
		for i := range asset.item.Markets {
			market := &asset.item.Markets[i]
			switch {
			case strings.EqualFold(market.MarketType, "spot"):
				if spot == nil || betterReference(*market, *spot, true) {
					spot = market
				}
			case strings.EqualFold(market.MarketType, "perp"):
				if perp == nil || betterReference(*market, *perp, false) {
					perp = market
				}
			}
		}
		if spot == nil || perp == nil {
			continue
		}
		item := model.CrossVenueItem{
			AssetID:             asset.item.AssetID,
			AssetSymbol:         asset.item.AssetSymbol,
			AssetName:           asset.item.AssetName,
			SpotMarketID:        spot.MarketID,
			SpotMarketCode:      spot.MarketCode,
			SpotExchange:        spot.Exchange,
			SpotQuoteAsset:      spot.QuoteAsset,
			SpotPrice:           spot.Price,
			SpotChange24h:       spot.Change24h,
			SpotChangeAvailable: spot.ChangeAvailable,
			SpotTurnover24h:     spot.Volume,
			SpotDelaySeconds:    spot.DataDelaySeconds,
			PerpMarketID:        perp.MarketID,
			PerpMarketCode:      perp.MarketCode,
			PerpExchange:        perp.Exchange,
			PerpQuoteAsset:      perp.QuoteAsset,
			PerpPrice:           perp.Price,
			PerpChange24h:       perp.Change24h,
			PerpChangeAvailable: perp.ChangeAvailable,
			PerpTurnover24h:     perp.Volume,
			PerpDelaySeconds:    perp.DataDelaySeconds,
		}
		combinedTurnover := new(big.Rat).Add(decimalRat(spot.Volume), decimalRat(perp.Volume))
		if combinedTurnover.Sign() > 0 {
			item.SpotTurnoverShare = ratDecimal(
				new(big.Rat).Mul(new(big.Rat).Quo(decimalRat(spot.Volume), combinedTurnover), big.NewRat(100, 1)), 4,
			)
			item.PerpTurnoverShare = ratDecimal(
				new(big.Rat).Mul(new(big.Rat).Quo(decimalRat(perp.Volume), combinedTurnover), big.NewRat(100, 1)), 4,
			)
		}
		spotPrice, perpPrice := decimalRat(spot.Price), decimalRat(perp.Price)
		if usdFamily(spot.QuoteAsset) && usdFamily(perp.QuoteAsset) &&
			spotPrice.Sign() > 0 && perpPrice.Sign() > 0 &&
			freshEnough(spot.DataDelaySeconds) && freshEnough(perp.DataDelaySeconds) {
			spread := new(big.Rat).Sub(perpPrice, spotPrice)
			spread.Quo(spread, spotPrice)
			spread.Mul(spread, big.NewRat(100, 1))
			item.IndicativeSpreadPct = ratDecimal(spread, 6)
			item.SpreadAvailable = true
		}
		if spot.ChangeAvailable && perp.ChangeAvailable {
			gap := new(big.Rat).Sub(decimalRat(perp.Change24h), decimalRat(spot.Change24h))
			item.ChangeGapPctPoints = ratDecimal(gap, 6)
			item.ChangeGapAvailable = true
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].AssetSymbol < result[j].AssetSymbol
	})
	return result
}

func freshEnough(delay int64) bool {
	return delay >= 0 && delay <= crossVenueFreshnessLimitSeconds
}

func usdFamily(symbol string) bool {
	switch strings.ToUpper(strings.TrimSpace(symbol)) {
	case "USD", "USDT", "USDC":
		return true
	default:
		return false
	}
}

func decimalRat(value string) *big.Rat {
	out := new(big.Rat)
	if _, ok := out.SetString(strings.TrimSpace(value)); !ok {
		return new(big.Rat)
	}
	return out
}

func ratDecimal(value *big.Rat, precision int) string {
	if value == nil {
		return "0"
	}
	out := value.FloatString(precision)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(out, "0")
		out = strings.TrimRight(out, ".")
	}
	if out == "" || out == "-" {
		return "0"
	}
	return out
}

func decimalFloat(value float64, precision int) string {
	out := strconv.FormatFloat(value, 'f', precision, 64)
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if out == "" || out == "-0" {
		return "0"
	}
	return out
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	mid := len(copied) / 2
	if len(copied)%2 == 1 {
		return copied[mid]
	}
	return (copied[mid-1] + copied[mid]) / 2
}
