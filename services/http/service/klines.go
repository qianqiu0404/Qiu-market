package service

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/services/http/model"
)

// parseKlineTimestamp extracts the real kline openTime from guid.
// guid format: "s1-1777462380000" where suffix is Binance openTime in ms.
// Falls back to createdAt if guid parsing fails.
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

// parseInterval converts interval string to minutes.
// Supported: "1m", "15m", "1h", "1d". Default: 1.
func parseInterval(s string) int64 {
	switch s {
	case "15m":
		return 15
	case "1h":
		return 60
	case "1d":
		return 1440
	default:
		return 1
	}
}

// parseScaledInt extracts the integer part of a numeric string (ignoring decimal).
// DB stores numeric(65,18), GORM may return "7709015000000" or "7709015000000.000000".
func parseScaledInt(s string) *big.Int {
	// Strip decimal part if present
	if idx := strings.Index(s, "."); idx >= 0 {
		s = s[:idx]
	}
	n := new(big.Int)
	n.SetString(s, 10)
	return n
}

// rawKline holds a parsed kline with raw (1e8-scaled) values.
type rawKline struct {
	timestamp         int64
	open, high, low, close, volume string // 1e8 scaled strings from DB
}

func (h HandleSvc) GetKlines(request *model.KlinesRequest) (*model.KlinesResponse, error) {
	// Determine interval
	intervalMinutes := parseInterval(request.Interval)
	intervalMs := intervalMinutes * 60 * 1000

	// Ensure limit is within reasonable range
	limit := request.Limit
	if limit < 20 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if limit == 0 {
		limit = 50
	}

	// For multi-interval aggregation, fetch more raw 1m data
	queryLimit := limit
	if intervalMinutes > 1 {
		// Fetch enough 1m data to produce `limit` aggregated bars, with 2x safety
		queryLimit = limit * intervalMinutes * 2
		if queryLimit > 10000 {
			queryLimit = 10000
		}
	}

	// Fetch raw 1m records
	list, _, err := h.symbolKlineView.QuerySymbolKlineList(1, queryLimit)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	minValid := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	maxValid := now.Add(24 * time.Hour)

	// Parse timestamps, filter anomalies, deduplicate
	rawMap := make(map[int64]*rawKline) // key: timestamp (ms)
	for _, k := range list {
		if k.SymbolGuid != request.SymbolGuid {
			continue
		}
		ts := parseKlineTimestamp(k.Guid, k.CreatedAt)
		tsTime := time.UnixMilli(ts)
		if tsTime.Before(minValid) || tsTime.After(maxValid) {
			continue
		}
		// Keep last occurrence on duplicate timestamp
		rawMap[ts] = &rawKline{
			timestamp: ts,
			open:   k.OpenPrice,
			high:   k.HighPrice,
			low:    k.LowPrice,
			close:  k.ClosePrice,
			volume: k.Volume,
		}
	}

	// Collect and sort by timestamp ASC
	sorted := make([]*rawKline, 0, len(rawMap))
	for _, r := range rawMap {
		sorted = append(sorted, r)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].timestamp < sorted[j].timestamp
	})

	if len(sorted) == 0 {
		return &model.KlinesResponse{
			Code:    2000,
			Message: "success",
			Result:  []model.KlineItem{},
		}, nil
	}

	// 1m: unscale and return directly
	if intervalMinutes <= 1 {
		var result []model.KlineItem
		for _, r := range sorted {
			result = append(result, model.KlineItem{
				Timestamp: r.timestamp,
				Open:      unscaleString(r.open, 8),
				High:      unscaleString(r.high, 8),
				Low:       unscaleString(r.low, 8),
				Close:     unscaleString(r.close, 8),
				Volume:    unscaleString(r.volume, 8),
			})
		}
		return &model.KlinesResponse{
			Code:    2000,
			Message: "success",
			Result:  result,
		}, nil
	}

	// Multi-interval aggregation
	type bucket struct {
		timestamp     int64
		open, high, low, close string
		volumeSum *big.Int
		first, last int // indices in sorted slice for open/close tracking
	}

	buckets := make(map[int64]*bucket)
	var bucketKeys []int64

	for i, r := range sorted {
		bucketTs := (r.timestamp / intervalMs) * intervalMs
		b, ok := buckets[bucketTs]
		if !ok {
			b = &bucket{
				timestamp: bucketTs,
				open:      r.open,
				high:      r.high,
				low:       r.low,
				close:     r.close,
				volumeSum: parseScaledInt(r.volume),
				first:     i,
				last:      i,
			}
			buckets[bucketTs] = b
			bucketKeys = append(bucketKeys, bucketTs)
		} else {
			// Update high (max)
			if compareScaledValues(r.high, b.high) > 0 {
				b.high = r.high
			}
			// Update low (min)
			if compareScaledValues(r.low, b.low) < 0 {
				b.low = r.low
			}
			// Close is always the last record in the bucket
			b.close = r.close
			// Sum volume using big.Int
			b.volumeSum = new(big.Int).Add(b.volumeSum, parseScaledInt(r.volume))
			b.last = i
		}
	}

	// Keep only the most recent `limit` buckets
	if len(bucketKeys) > int(limit) {
		bucketKeys = bucketKeys[len(bucketKeys)-int(limit):]
	}

	// Build result: unscale each aggregated value
	var result []model.KlineItem
	for _, bt := range bucketKeys {
		b := buckets[bt]
		// Volume sum is already a big.Int (scaled), convert to string for unscale
		volStr := b.volumeSum.String()
		result = append(result, model.KlineItem{
			Timestamp: b.timestamp,
			Open:      unscaleString(b.open, 8),
			High:      unscaleString(b.high, 8),
			Low:       unscaleString(b.low, 8),
			Close:     unscaleString(b.close, 8),
			Volume:    unscaleString(volStr, 8),
		})
	}

	return &model.KlinesResponse{
		Code:    2000,
		Message: "success",
		Result:  result,
	}, nil
}

// compareScaledValues compares two 1e8 scaled string values.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareScaledValues(a, b string) int {
	ai := parseScaledInt(a)
	bi := parseScaledInt(b)
	return ai.Cmp(bi)
}
