package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

// parseKlineTimestamp extracts the real kline openTime from guid.
// Supports both the legacy "s1-1777462380000" format and the current
// "s1-15m-1777462380000" format: the suffix after the LAST "-" is the
// Binance openTime in ms. Falls back to createdAt if guid parsing fails.
func parseKlineTimestamp(guid string, createdAt time.Time) int64 {
	idx := strings.LastIndex(guid, "-")
	if idx < 0 || idx+1 >= len(guid) {
		return createdAt.UnixMilli()
	}
	suffix := guid[idx+1:]
	ts, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil {
		return createdAt.UnixMilli()
	}
	minValid := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	maxValid := time.Now().Add(24 * time.Hour).UnixMilli()
	if ts < minValid || ts > maxValid {
		return createdAt.UnixMilli()
	}
	return ts
}

// GetKlines returns native per-interval klines stored by the crawler.
// No aggregation and no cross-symbol filtering happen here: rows are
// queried directly by (symbol_guid, interval).
func (h HandleSvc) GetKlines(request *model.KlinesRequest) (*model.KlinesResponse, error) {
	// Validate interval (default 1m)
	interval := request.Interval
	switch interval {
	case "1m", "15m", "1h", "1d":
	default:
		interval = "1m"
	}

	// Ensure limit is within reasonable range
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit < 20 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	var (
		list []*database.SymbolKline
		err  error
	)
	if marketID := strings.TrimSpace(request.MarketID); marketID != "" {
		list, err = h.symbolKlineView.QueryMarketKlines(marketID, interval, int(limit))
	} else {
		legacyMarket, resolveErr := h.exchangeSymbolView.QueryUniqueActiveMarketBySymbol(strings.TrimSpace(request.SymbolGuid))
		if resolveErr != nil {
			return nil, resolveErr
		}
		list, err = h.symbolKlineView.QueryMarketKlines(legacyMarket.Guid, interval, int(limit))
	}
	if err != nil {
		return nil, err
	}

	if len(list) == 0 {
		return &model.KlinesResponse{
			Code:    2000,
			Message: "success",
			Result:  []model.KlineItem{},
		}, nil
	}

	// Rows come back open_time DESC; charting always receives business time ASC.
	sort.Slice(list, func(i, j int) bool {
		return klineOpenTime(list[i]).Before(klineOpenTime(list[j]))
	})

	result := make([]model.KlineItem, 0, len(list))
	for _, k := range list {
		result = append(result, model.KlineItem{
			Timestamp: klineOpenTime(k).UnixMilli(),
			Open:      unscaleString(k.OpenPrice, 8),
			High:      unscaleString(k.HighPrice, 8),
			Low:       unscaleString(k.LowPrice, 8),
			Close:     unscaleString(k.ClosePrice, 8),
			Volume:    unscaleString(k.Volume, 8),
		})
	}

	return &model.KlinesResponse{
		Code:    2000,
		Message: "success",
		Result:  result,
	}, nil
}

func klineOpenTime(k *database.SymbolKline) time.Time {
	if !k.OpenTime.IsZero() {
		return k.OpenTime
	}
	return time.UnixMilli(parseKlineTimestamp(k.Guid, k.CreatedAt))
}

func (h HandleSvc) GetMarketSparklines(request *model.MarketSparklinesRequest) (*model.MarketSparklinesResponse, error) {
	if len(request.MarketIDs) == 0 {
		return &model.MarketSparklinesResponse{
			Code: 2000, Message: "success", Result: []model.MarketSparkline{},
		}, nil
	}
	if len(request.MarketIDs) > 100 {
		request.MarketIDs = request.MarketIDs[:100]
	}
	interval := request.Interval
	switch interval {
	case "1m", "15m", "1h", "1d":
	default:
		interval = "1h"
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 168
	}
	if limit > 500 {
		limit = 500
	}
	pointsByMarket, err := h.symbolKlineView.QueryMarketSparklines(request.MarketIDs, interval, int(limit))
	if err != nil {
		return nil, err
	}
	result := make([]model.MarketSparkline, 0, len(request.MarketIDs))
	for _, marketID := range request.MarketIDs {
		points := pointsByMarket[marketID]
		if len(points) == 0 {
			continue
		}
		item := model.MarketSparkline{
			MarketID: marketID,
			Points:   make([]model.SparklinePoint, 0, len(points)),
		}
		for _, point := range points {
			item.Points = append(item.Points, model.SparklinePoint{
				Timestamp: point.OpenTime.UnixMilli(),
				Close:     unscaleString(point.ClosePrice, 8),
			})
		}
		result = append(result, item)
	}
	return &model.MarketSparklinesResponse{
		Code: 2000, Message: "success", Result: result,
	}, nil
}
