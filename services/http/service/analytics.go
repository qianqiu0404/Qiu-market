package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

// ErrDorisUnavailable 表示数仓未配置或不可达。路由层据此返回标准错误信封，
// 前端渲染 ErrorState —— 分析接口不回退 PG 现算，也不造假数据。
var ErrDorisUnavailable = errors.New("doris data warehouse is unavailable")

// klineAnalyticsQuery 在 Doris 侧一次完成全部聚合：
//   - a 子查询：每个 symbol 的蜡烛数、周期涨跌幅（末根收盘 vs 首根开盘，用
//     max_by / min_by 按 open_time 取首末）、最高 / 最低 / 振幅、均量 / 总量；
//   - b 子查询：窗口函数 LAG 计算相邻收盘收益率，再按 symbol 求 STDDEV_POP
//     作为波动率（首根 candle 的 ret 为 NULL，STDDEV_POP 自动忽略）。
//
// 全部过滤 / 聚合 / 排序都在 Doris（列存 + MPP）执行，Go 侧只做行扫描。
const klineAnalyticsQuery = `
SELECT a.symbol_guid,
       a.candle_count,
       a.price_change_pct,
       a.period_high,
       a.period_low,
       a.high_low_range,
       b.volatility_pct,
       a.avg_volume,
       a.total_volume
FROM (
    SELECT symbol_guid,
           COUNT(*) AS candle_count,
           ROUND((max_by(close_price, open_time) - min_by(open_price, open_time))
                 / NULLIF(min_by(open_price, open_time), 0) * 100, 4) AS price_change_pct,
           MAX(high_price) AS period_high,
           MIN(low_price)  AS period_low,
           ROUND(MAX(high_price) - MIN(low_price), 8) AS high_low_range,
           ROUND(AVG(volume), 4) AS avg_volume,
           ROUND(SUM(volume), 4) AS total_volume
    FROM dwd_symbol_kline
    WHERE ` + "`interval`" + ` = ? AND open_time >= ? AND open_time < ?
    GROUP BY symbol_guid
) a
LEFT JOIN (
    SELECT symbol_guid, ROUND(STDDEV_POP(ret) * 100, 4) AS volatility_pct
    FROM (
        SELECT symbol_guid,
               close_price / NULLIF(LAG(close_price) OVER (PARTITION BY symbol_guid ORDER BY open_time), 0) - 1 AS ret
        FROM dwd_symbol_kline
        WHERE ` + "`interval`" + ` = ? AND open_time >= ? AND open_time < ?
    ) r
    GROUP BY symbol_guid
) b ON a.symbol_guid = b.symbol_guid
ORDER BY a.total_volume DESC
LIMIT ?
`

const assetMomentumQuery = `
WITH candles AS (
    SELECT symbol_guid,
           open_time,
           MAX(open_price) AS open_price,
           MAX(high_price) AS high_price,
           MAX(low_price) AS low_price,
           MAX(close_price) AS close_price
    FROM dwd_symbol_kline
    WHERE ` + "`interval`" + ` = '1h' AND open_time >= ? AND open_time < ?
    GROUP BY symbol_guid, open_time
),
returns AS (
    SELECT symbol_guid,
           open_time,
           open_price,
           high_price,
           low_price,
           close_price,
           close_price / NULLIF(LAG(close_price) OVER (
               PARTITION BY symbol_guid ORDER BY open_time
           ), 0) - 1 AS hourly_return
    FROM candles
)
SELECT symbol_guid,
       COUNT(*) AS candle_count,
       ROUND((max_by(close_price, open_time) - min_by(open_price, open_time))
             / NULLIF(min_by(open_price, open_time), 0) * 100, 6) AS return_pct,
       ROUND(STDDEV_POP(hourly_return) * 100, 6) AS volatility_pct,
       ROUND((MAX(high_price) - MIN(low_price))
             / NULLIF(MIN(low_price), 0) * 100, 6) AS high_low_range_pct
FROM returns
GROUP BY symbol_guid
ORDER BY symbol_guid
`

// GetKlineAnalytics 查询 Doris 数仓返回每个交易对的 K 线聚合分析。
// dorisDB 为 nil（未配置 / 启动时不可达）或查询失败时返回 ErrDorisUnavailable
// 包装错误 —— 分析接口显式报错，绝不回退 PG 现算。
func (h HandleSvc) GetKlineAnalytics(request *model.KlineAnalyticsRequest) (*model.KlineAnalyticsResponse, error) {
	if h.dorisDB == nil {
		return nil, fmt.Errorf("%w: not configured or unreachable at api startup (check the compose doris service and MARKET_DORIS_* env)", ErrDorisUnavailable)
	}

	interval := request.Interval
	switch interval {
	case "1m", "15m", "1h", "1d":
	default:
		interval = "1m"
	}

	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	windowEnd := closedWindowEnd(time.Now(), interval)
	windowStart := windowEnd.Add(-30 * 24 * time.Hour)
	rows, err := h.dorisDB.QueryContext(
		ctx,
		klineAnalyticsQuery,
		interval, windowStart, windowEnd,
		interval, windowStart, windowEnd,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDorisUnavailable, err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]model.KlineAnalyticsItem, 0, limit)
	for rows.Next() {
		var (
			item                                model.KlineAnalyticsItem
			priceChange, volatility             sql.NullString
			periodHigh, periodLow, highLowRange sql.NullString
			avgVolume, totalVolume              sql.NullString
		)
		if err := rows.Scan(
			&item.SymbolGuid,
			&item.CandleCount,
			&priceChange,
			&periodHigh,
			&periodLow,
			&highLowRange,
			&volatility,
			&avgVolume,
			&totalVolume,
		); err != nil {
			return nil, fmt.Errorf("%w: scan row: %v", ErrDorisUnavailable, err)
		}
		item.PriceChangePct = nullDecimal(priceChange)
		item.PeriodHigh = nullDecimal(periodHigh)
		item.PeriodLow = nullDecimal(periodLow)
		item.HighLowRange = nullDecimal(highLowRange)
		item.VolatilityPct = nullDecimal(volatility)
		item.AvgVolume = nullDecimal(avgVolume)
		item.TotalVolume = nullDecimal(totalVolume)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDorisUnavailable, err)
	}

	// 交易对名称映射：与 top movers 同一模式（symbol + asset 两表在内存 join）。
	// 分析结果集只有 limit 行，两张表全量也就几百行，开销可忽略；
	// 映射失败保留 guid，绝不让名称查询拖垮分析接口。
	if symbols, err := h.symbolView.QuerySymbols(); err == nil {
		symbolMap := make(map[string]*database.Symbol, len(symbols))
		for _, s := range symbols {
			symbolMap[s.Guid] = s
		}
		assetMap := make(map[string]string)
		if assets, err := h.assetView.QueryAssets(); err == nil {
			for _, a := range assets {
				assetMap[a.Guid] = a.AssetName
			}
		}
		for i := range result {
			sym, ok := symbolMap[result[i].SymbolGuid]
			if !ok {
				result[i].SymbolName = result[i].SymbolGuid
				continue
			}
			result[i].SymbolName = sym.SymbolName
			result[i].BaseAsset = assetMap[sym.BaseAssetGuid]
			result[i].QuoteAsset = assetMap[sym.QuoteAssetGuid]
		}
	} else {
		for i := range result {
			result[i].SymbolName = result[i].SymbolGuid
		}
	}

	return &model.KlineAnalyticsResponse{
		Code:     2000,
		Message:  "get kline analytics success",
		Result:   result,
		Total:    int64(len(result)),
		Interval: interval,
	}, nil
}

func (h HandleSvc) GetAssetMomentum(request *model.AssetMomentumRequest) (*model.AssetMomentumResponse, error) {
	if h.dorisDB == nil {
		return nil, fmt.Errorf("%w: not configured or unreachable at api startup (check the compose doris service and MARKET_DORIS_* env)", ErrDorisUnavailable)
	}
	window, duration, expected := momentumWindow(request.Window)
	windowEnd := time.Now().UTC().Truncate(time.Hour)
	windowStart := windowEnd.Add(-duration)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := h.dorisDB.QueryContext(ctx, assetMomentumQuery, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDorisUnavailable, err)
	}
	defer func() { _ = rows.Close() }()

	type rawMomentum struct {
		symbolGuid   string
		candleCount  int64
		returnPct    string
		volatility   string
		highLowRange string
	}
	rawBySymbol := make(map[string]rawMomentum)
	for rows.Next() {
		var (
			symbolGuid                          string
			candleCount                         int64
			returnPct, volatility, highLowRange sql.NullString
		)
		if err := rows.Scan(&symbolGuid, &candleCount, &returnPct, &volatility, &highLowRange); err != nil {
			return nil, fmt.Errorf("%w: scan momentum row: %v", ErrDorisUnavailable, err)
		}
		rawBySymbol[symbolGuid] = rawMomentum{
			symbolGuid:   symbolGuid,
			candleCount:  candleCount,
			returnPct:    nullDecimal(returnPct),
			volatility:   nullDecimal(volatility),
			highLowRange: nullDecimal(highLowRange),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDorisUnavailable, err)
	}

	marketRows, err := h.symbolMarketView.QueryMarketReadRows("")
	if err != nil {
		return nil, err
	}
	rowByMarket := make(map[string]database.MarketReadRow, len(marketRows))
	for _, row := range marketRows {
		rowByMarket[row.MarketID] = row
	}
	assets := aggregateAssetRows(marketRows, nil, nil)
	result := make([]model.AssetMomentumItem, 0)
	for _, asset := range assets {
		marketRow, ok := rowByMarket[asset.item.ReferenceMarketID]
		if !ok {
			continue
		}
		raw, ok := rawBySymbol[marketRow.SymbolGuid]
		if !ok {
			continue
		}
		coverage := momentumCoverage(raw.candleCount, expected)
		result = append(result, model.AssetMomentumItem{
			AssetID:         asset.item.AssetID,
			AssetSymbol:     asset.item.AssetSymbol,
			AssetName:       asset.item.AssetName,
			MarketID:        asset.item.ReferenceMarketID,
			MarketCode:      asset.item.ReferenceMarketCode,
			Exchange:        asset.item.ReferenceExchange,
			ReturnPct:       raw.returnPct,
			VolatilityPct:   raw.volatility,
			HighLowRangePct: raw.highLowRange,
			CandleCount:     raw.candleCount,
			ExpectedCandles: expected,
			CoveragePct:     decimalFloat(coverage, 4),
			LowCoverage:     coverage < 90,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LowCoverage != result[j].LowCoverage {
			return !result[i].LowCoverage
		}
		left, _ := strconv.ParseFloat(result[i].ReturnPct, 64)
		right, _ := strconv.ParseFloat(result[j].ReturnPct, 64)
		if left == right {
			return result[i].AssetID < result[j].AssetID
		}
		return left > right
	})

	return &model.AssetMomentumResponse{
		Code:        2000,
		Message:     "get asset momentum success",
		Result:      result,
		Total:       int64(len(result)),
		Window:      window,
		Interval:    "1h",
		WindowStart: windowStart.UnixMilli(),
		WindowEnd:   windowEnd.UnixMilli(),
	}, nil
}

func momentumWindow(value string) (string, time.Duration, int64) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "24h":
		return "24h", 24 * time.Hour, 24
	case "30d":
		return "30d", 30 * 24 * time.Hour, 720
	default:
		return "7d", 7 * 24 * time.Hour, 168
	}
}

func momentumCoverage(candles, expected int64) float64 {
	if candles <= 0 || expected <= 0 {
		return 0
	}
	coverage := float64(candles) / float64(expected) * 100
	if coverage > 100 {
		return 100
	}
	return coverage
}

func closedWindowEnd(now time.Time, interval string) time.Time {
	now = now.UTC()
	switch interval {
	case "1d":
		return now.Truncate(24 * time.Hour)
	case "1h":
		return now.Truncate(time.Hour)
	case "15m":
		return now.Truncate(15 * time.Minute)
	default:
		return now.Truncate(time.Minute)
	}
}

// nullDecimal 规整 Doris DECIMAL 列的扫描值：Doris 会把 DECIMAL 序列化成
// 定长小数字符串（如 "1.376600000000"），这里去掉尾部多余的 0；
// NULL（如只有 1 根 candle 时的波动率）统一为 "0"，前端无需处理 null。
func nullDecimal(v sql.NullString) string {
	if !v.Valid {
		return "0"
	}
	s := strings.TrimSpace(v.String)
	if s == "" {
		return "0"
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
