package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/markettime"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
	"github.com/the-web3/s78-market-services/redis"
)

// Binance 24hr ticker 响应字段
type binanceTicker struct {
	Symbol             string `json:"symbol"`
	LastPrice          string `json:"lastPrice"`
	BidPrice           string `json:"bidPrice"`
	AskPrice           string `json:"askPrice"`
	PriceChangePercent string `json:"priceChangePercent"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
	CloseTime          int64  `json:"closeTime"`
}

// CoinGecko 价格响应
type coingeckoPriceResponse map[string]struct {
	Usd           float64 `json:"usd"`
	UsdMarketCap  float64 `json:"usd_market_cap"`
	Usd24hVol     float64 `json:"usd_24h_vol"`
	Usd24hChange  float64 `json:"usd_24h_change"`
	LastUpdatedAt int64   `json:"last_updated_at"`
}

// K-line collection remains deliberately limited to these six markets while
// spot ticker coverage expands through the reviewed provider catalog.
var legacyBinanceCatalog = map[string]string{
	"BTCUSDT":  "s1",
	"ETHUSDT":  "s2",
	"SOLUSDT":  "s3",
	"BNBUSDT":  "s4",
	"XRPUSDT":  "s5",
	"DOGEUSDT": "s6",
}

// K线按周期原生存储
var klineIntervals = []string{"1m", "15m", "1h", "1d"}

// 首次回填（无历史数据时）的回看窗口
var klineBackfillLookback = map[string]time.Duration{
	"1m":  2 * 24 * time.Hour,
	"15m": 7 * 24 * time.Hour,
	"1h":  30 * 24 * time.Hour,
	"1d":  180 * 24 * time.Hour,
}

// 增量刷新节奏：每个周期只拉最新几根 K 线
var klineRefreshCadence = map[string]time.Duration{
	"1m":  30 * time.Second,
	"15m": 2 * time.Minute,
	"1h":  10 * time.Minute,
	"1d":  time.Hour,
}

type BinanceTickerCrawler struct {
	db         *database.DB
	writer     *marketdata.SnapshotWriter
	reporter   *marketdata.ProviderReporter
	httpClient *http.Client
	stopped    atomic.Bool
	cancel     context.CancelFunc
	// CoinGecko 调用节流
	lastCGTime time.Time
	// 每个 K 线周期的上次增量刷新时间
	lastKlineFetch map[string]time.Time
	markets        []database.ProviderMarket
}

func NewBinanceTickerCrawler(db *database.DB, redisClient *redis.Client) *BinanceTickerCrawler {
	return &BinanceTickerCrawler{
		db:             db,
		writer:         marketdata.NewSnapshotWriter(db, redisClient),
		reporter:       marketdata.NewProviderReporter(db.ProviderStatus),
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		lastKlineFetch: make(map[string]time.Time),
	}
}

func (b *BinanceTickerCrawler) Start() error {
	if err := b.loadProviderCatalog(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	go b.runLoop(ctx)
	log.Info("BinanceTickerCrawler started")
	return nil
}

func (b *BinanceTickerCrawler) loadProviderCatalog() error {
	rows, err := b.db.ExchangeSymbol.QueryProviderMarkets("binance")
	if err != nil {
		return fmt.Errorf("load Binance provider catalog: %w", err)
	}
	active, err := validateBinanceCatalog(rows)
	if err != nil {
		return err
	}
	b.markets = active
	log.Info("Binance provider catalog loaded",
		"markets", len(active), "source", "exchange_symbol.source_symbol")
	return nil
}

func validateBinanceCatalog(rows []database.ProviderMarket) ([]database.ProviderMarket, error) {
	active := make([]database.ProviderMarket, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(row.MarketType, "spot") {
			return nil, fmt.Errorf("Binance catalog rejects unsupported market type %q for %s",
				row.MarketType, row.MarketCode)
		}
		source := strings.TrimSpace(row.SourceSymbol)
		if source == "" {
			return nil, fmt.Errorf("Binance catalog source_symbol is missing for %s", row.MarketCode)
		}
		if _, duplicate := seen[source]; duplicate {
			return nil, fmt.Errorf("Binance catalog source_symbol %q is duplicated", source)
		}
		seen[source] = struct{}{}
		active = append(active, row)
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].SourceSymbol < active[j].SourceSymbol
	})
	return active, nil
}

func (b *BinanceTickerCrawler) klineMarkets() []database.ProviderMarket {
	result := make([]database.ProviderMarket, 0, len(legacyBinanceCatalog))
	for _, market := range b.markets {
		expectedSymbolGuid, legacy := legacyBinanceCatalog[market.SourceSymbol]
		if legacy && expectedSymbolGuid == market.SymbolGuid {
			result = append(result, market)
		}
	}
	return result
}

func (b *BinanceTickerCrawler) Stop() error {
	if b.cancel != nil {
		b.cancel()
	}
	b.stopped.Store(true)
	log.Info("BinanceTickerCrawler stopped")
	return nil
}

func (b *BinanceTickerCrawler) Stopped() bool {
	return b.stopped.Load()
}

func (b *BinanceTickerCrawler) runLoop(ctx context.Context) {
	// 启动时先做一次全量回填，补齐停机期间缺失的 K 线
	b.backfillAllKlines(ctx)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// K 线增量刷新（各周期按各自节奏，只拉最新几根）
			b.refreshKlines()
			b.processRepairTasks(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (b *BinanceTickerCrawler) fetchAndStore() {
	client := b.httpClient

	for _, marketConfig := range b.markets {
		symbol := marketConfig.SourceSymbol
		guid := marketConfig.SymbolGuid
		sourceKey := "ticker:" + symbol
		attemptedAt := time.Now().UTC()
		b.reporter.Attempt("binance", sourceKey, attemptedAt)

		url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/24hr?symbol=%s", symbol)
		resp, err := client.Get(url)
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, 0)
			log.Error("BinanceTickerCrawler HTTP request failed", "symbol", symbol, "error", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler read body failed", "symbol", symbol, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			statusErr := fmt.Errorf("Binance ticker HTTP %d", resp.StatusCode)
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), statusErr, resp.StatusCode)
			log.Error("BinanceTickerCrawler bad status", "symbol", symbol, "status", resp.StatusCode)
			continue
		}

		var ticker binanceTicker
		if err := json.Unmarshal(body, &ticker); err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler JSON parse failed", "symbol", symbol, "error", err)
			continue
		}

		if ticker.LastPrice == "" || ticker.Volume == "" {
			valueErr := fmt.Errorf("Binance ticker missing price or volume")
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), valueErr, resp.StatusCode)
			log.Warn("BinanceTickerCrawler empty price/volume", "symbol", symbol)
			continue
		}

		// 将小数转为整数，放大 1e8
		scaledPrice, err := decimalStringToUint256String(ticker.LastPrice, 8)
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler scale price failed", "symbol", symbol, "price", ticker.LastPrice, "error", err)
			continue
		}
		// 使用 quoteVolume（USDT 计价）替代 base volume，展示更有意义的交易量
		volString := ticker.QuoteVolume
		if volString == "" {
			volString = ticker.Volume // fallback
		}
		scaledVolume, err := decimalStringToUint256String(volString, 8)
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler scale volume failed", "symbol", symbol, "volume", volString, "error", err)
			continue
		}
		askPrice := fallbackString(ticker.AskPrice, ticker.LastPrice)
		bidPrice := fallbackString(ticker.BidPrice, ticker.LastPrice)
		scaledAskPrice, err := decimalStringToUint256String(askPrice, 8)
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler scale ask price failed", "symbol", symbol, "askPrice", askPrice, "error", err)
			continue
		}
		scaledBidPrice, err := decimalStringToUint256String(bidPrice, 8)
		if err != nil {
			b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
			log.Error("BinanceTickerCrawler scale bid price failed", "symbol", symbol, "bidPrice", bidPrice, "error", err)
			continue
		}

		observedAt := time.Now().UTC()
		var sourceTime *time.Time
		var sourceTimeKind *string
		if ticker.CloseTime > 0 {
			value := time.UnixMilli(ticker.CloseTime).UTC()
			kind := "ticker_window_close"
			sourceTime = &value
			sourceTimeKind = &kind
		}
		change := ticker.PriceChangePercent
		result, err := b.writer.Write(context.Background(), marketdata.Snapshot{
			MarketSnapshotInput: database.MarketSnapshotInput{
				Guid:           "m-" + guid,
				MarketID:       marketConfig.MarketID,
				SymbolGuid:     guid,
				Price:          scaledPrice,
				AskPrice:       scaledAskPrice,
				BidPrice:       scaledBidPrice,
				Volume:         scaledVolume,
				Change24hPct:   &change,
				IsActive:       true,
				ObservedAt:     observedAt,
				SourceTime:     sourceTime,
				SourceTimeKind: sourceTimeKind,
			},
			ExchangeGuid: "e1",
			ExchangeName: "Binance",
			SymbolName:   binanceSymbolToPair(symbol),
		})
		if err != nil {
			log.Error("BinanceTickerCrawler snapshot write failed",
				"symbol", symbol, "market_id", marketConfig.MarketID, "error", err)
			continue
		}
		b.reporter.Success("binance", sourceKey, observedAt, sourceTime)

		log.Info("BinanceTickerCrawler observed",
			"symbol", symbol, "market_id", marketConfig.MarketID,
			"action", result.Action, "price", scaledPrice, "volume", scaledVolume)
	}
}

func fallbackString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func binanceSymbolToPair(symbol string) string {
	if len(symbol) > 4 && symbol[len(symbol)-4:] == "USDT" {
		return symbol[:len(symbol)-4] + "/USDT"
	}
	return symbol
}

// fetchAndStoreMarketCap 从 CoinGecko 获取市值数据并写入 DB
func (b *BinanceTickerCrawler) fetchAndStoreMarketCap() {
	mappings, err := b.db.ExchangeSymbol.QueryAssetExternalMappings("coingecko")
	if err != nil {
		log.Error("CoinGecko catalog query failed", "error", err)
		return
	}
	externalByAsset := make(map[string]string, len(mappings))
	ids := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		externalByAsset[mapping.AssetGuid] = mapping.ExternalID
		ids = append(ids, mapping.ExternalID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		log.Error("CoinGecko catalog is empty")
		return
	}
	sourceKey := "simple-price"
	attemptedAt := time.Now().UTC()
	b.reporter.Attempt("coingecko", sourceKey, attemptedAt)
	url := fmt.Sprintf(
		"https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_vol=true&include_24hr_change=true&include_market_cap=true&include_last_updated_at=true",
		strings.Join(ids, ","),
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		b.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), err, 0)
		log.Error("CoinGecko request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("CoinGecko HTTP %d", resp.StatusCode)
		b.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), statusErr, resp.StatusCode)
		log.Error("CoinGecko bad status", "status", resp.StatusCode)
		return
	}

	var data coingeckoPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		b.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), err, resp.StatusCode)
		log.Error("CoinGecko decode failed", "error", err)
		return
	}

	var latestSourceTime *time.Time
	for _, market := range b.klineMarkets() {
		coinID, ok := externalByAsset[market.BaseAssetID]
		if !ok {
			log.Warn("CoinGecko mapping missing for asset",
				"asset_id", market.BaseAssetID, "market_id", market.MarketID)
			continue
		}
		info, ok := data[coinID]
		if !ok {
			log.Warn("CoinGecko no data for coin", "coin_id", coinID, "market_id", market.MarketID)
			continue
		}
		// market_cap 转为 uint256 字符串（1e8 放大）
		mcStr := fmt.Sprintf("%.0f", info.UsdMarketCap)
		scaledMC, err := decimalStringToUint256String(mcStr, 8)
		if err != nil {
			log.Error("CoinGecko scale market_cap failed", "coin", coinID, "market_cap", mcStr, "error", err)
			continue
		}
		if err := b.db.SymbolMarket.UpdateSymbolMarketFull(market.SymbolGuid, scaledMC); err != nil {
			log.Error("CoinGecko DB update failed", "market_id", market.MarketID, "error", err)
		}
		if info.LastUpdatedAt > 0 {
			value := time.Unix(info.LastUpdatedAt, 0).UTC()
			if latestSourceTime == nil || value.After(*latestSourceTime) {
				latestSourceTime = &value
			}
		}
		log.Info("CoinGecko market_cap updated", "market_id", market.MarketID, "market_cap", mcStr)
	}
	b.reporter.Success("coingecko", sourceKey, time.Now().UTC(), latestSourceTime)
}

// backfillAllKlines 在 crawler 启动时执行一次，把每个 symbol+interval 的历史 K 线
// 从本地最新一根之后（或按周期默认回看窗口）补齐到当前。单个 symbol/interval
// 失败不会影响其他的回填。
func (b *BinanceTickerCrawler) backfillAllKlines(ctx context.Context) {
	log.Info("BinanceTickerCrawler kline backfill started")
	for _, market := range b.klineMarkets() {
		for _, interval := range klineIntervals {
			if ctx.Err() != nil {
				log.Info("BinanceTickerCrawler kline backfill aborted", "err", ctx.Err())
				return
			}
			b.backfillKline(market, interval)
		}
	}
	// 回填完成后锚定增量刷新时间，按各自节奏错峰
	now := time.Now()
	for _, interval := range klineIntervals {
		b.lastKlineFetch[interval] = now
	}
	log.Info("BinanceTickerCrawler kline backfill finished")
}

// backfillKline 分页回填单个 symbol+interval，直到追上当前时间
func (b *BinanceTickerCrawler) backfillKline(market database.ProviderMarket, interval string) {
	symbol := market.SourceSymbol
	guid := market.SymbolGuid
	lookback := klineBackfillLookback[interval]
	if lookback <= 0 {
		lookback = 2 * 24 * time.Hour
	}
	startTime := time.Now().Add(-lookback).UnixMilli()

	latest, err := b.db.SymbolKline.QueryLatestMarketKline(market.MarketID, interval)
	if err != nil {
		log.Error("kline backfill query latest failed, fallback to lookback window", "symbol", symbol, "interval", interval, "err", err)
	} else if latest != nil {
		if ts := klineOpenTimeMs(latest); ts > 0 {
			startTime = ts + 1
		}
	}

	total := 0
	for {
		endTime := time.Now().UnixMilli()
		if startTime >= endTime {
			break
		}
		klines, err := b.fetchKlines(symbol, interval, startTime, endTime, 1000)
		if err != nil {
			// 网络错误：记录后中断本次回填，已落库的数据保留，下次启动会续传
			log.Error("kline backfill fetch failed", "symbol", symbol, "interval", interval, "err", err)
			break
		}
		if len(klines) == 0 {
			break
		}
		lastOpen := b.storeKlines(market.MarketID, guid, interval, klines)
		total += len(klines)
		if len(klines) < 1000 {
			break
		}
		if lastOpen < startTime {
			// 防御：openTime 没有前进，避免死循环
			break
		}
		startTime = lastOpen + 1
	}
	log.Info("kline backfill done", "symbol", symbol, "interval", interval, "candles", total)
}

// refreshKlines 按周期错峰增量刷新，每次只拉最新 3 根（进行中的 K 线通过 upsert 更新）
func (b *BinanceTickerCrawler) refreshKlines() {
	now := time.Now()
	for _, interval := range klineIntervals {
		cadence := klineRefreshCadence[interval]
		if cadence <= 0 {
			cadence = 30 * time.Second
		}
		if now.Sub(b.lastKlineFetch[interval]) < cadence {
			continue
		}
		b.lastKlineFetch[interval] = now
		for _, market := range b.klineMarkets() {
			symbol := market.SourceSymbol
			klines, err := b.fetchKlines(symbol, interval, 0, 0, 3)
			if err != nil {
				log.Error("kline refresh fetch failed", "symbol", symbol, "interval", interval, "err", err)
				continue
			}
			b.storeKlines(market.MarketID, market.SymbolGuid, interval, klines)
		}
	}
}

// fetchKlines 调用 Binance klines API。startTime/endTime 传 0 表示不带该参数
// （不带 startTime 时 Binance 返回最近的 limit 根）。
func (b *BinanceTickerCrawler) fetchKlines(symbol, interval string, startTime, endTime int64, limit int) ([][]interface{}, error) {
	sourceKey := "kline:" + symbol + ":" + interval
	attemptedAt := time.Now().UTC()
	b.reporter.Attempt("binance", sourceKey, attemptedAt)
	url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=%s&limit=%d", symbol, interval, limit)
	if startTime > 0 {
		url += fmt.Sprintf("&startTime=%d", startTime)
	}
	if endTime > 0 {
		url += fmt.Sprintf("&endTime=%d", endTime)
	}

	resp, err := b.httpClient.Get(url)
	if err != nil {
		b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, 0)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		err := fmt.Errorf("Binance klines HTTP %d", resp.StatusCode)
		b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
		return nil, err
	}

	var klines [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&klines); err != nil {
		b.reporter.Failure("binance", sourceKey, time.Now().UTC(), err, resp.StatusCode)
		return nil, err
	}
	b.reporter.Success("binance", sourceKey, time.Now().UTC(), nil)
	return klines, nil
}

func (b *BinanceTickerCrawler) processRepairTasks(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := time.Now().UTC()
	tasks, err := b.db.KlineRepair.ClaimRepairTasks("binance", 5, now)
	if err != nil {
		log.Error("Binance repair task claim failed", "error", err)
		return
	}
	for _, task := range tasks {
		market, ok := b.providerMarketByID(task.MarketID)
		if !ok || market.SymbolGuid == "" || task.SourceSymbol == "" {
			_ = b.db.KlineRepair.RetryRepairTask(
				task.TaskKey,
				"repair task market is absent from the validated Binance catalog",
				now,
				true,
			)
			continue
		}
		klines, fetchErr := b.fetchKlines(
			task.SourceSymbol,
			task.Interval,
			task.GapStart.UnixMilli(),
			task.GapEnd.Add(-time.Millisecond).UnixMilli(),
			1000,
		)
		if fetchErr != nil {
			permanent := task.AttemptCount >= 8
			retryAt := now.Add(repairRetryDelay(task.AttemptCount))
			if err := b.db.KlineRepair.RetryRepairTask(
				task.TaskKey, fetchErr.Error(), retryAt, permanent,
			); err != nil {
				log.Error("Binance repair task retry update failed",
					"task_key", task.TaskKey, "error", err)
			}
			continue
		}
		b.storeKlines(task.MarketID, market.SymbolGuid, task.Interval, klines)
		duration, durationErr := markettime.Duration(task.Interval)
		openTimes, queryErr := b.db.KlineRepair.QueryKlineOpenTimes(
			task.MarketID, task.Interval, task.GapStart, task.GapEnd,
		)
		if durationErr != nil || queryErr != nil ||
			!repairRangeComplete(task.GapStart, task.GapEnd, duration, openTimes) {
			verifyErr := queryErr
			if verifyErr == nil {
				verifyErr = durationErr
			}
			if verifyErr == nil {
				verifyErr = fmt.Errorf(
					"repair range remains incomplete after fetch: expected=%d actual=%d",
					expectedRepairCandles(task.GapStart, task.GapEnd, duration),
					len(openTimes),
				)
			}
			permanent := task.AttemptCount >= 8
			retryAt := now.Add(repairRetryDelay(task.AttemptCount))
			if err := b.db.KlineRepair.RetryRepairTask(
				task.TaskKey, verifyErr.Error(), retryAt, permanent,
			); err != nil {
				log.Error("Binance repair task verification update failed",
					"task_key", task.TaskKey, "error", err)
			}
			continue
		}
		if err := b.db.KlineRepair.CompleteRepairTask(task.TaskKey, time.Now().UTC()); err != nil {
			log.Error("Binance repair task completion failed",
				"task_key", task.TaskKey, "error", err)
			continue
		}
		log.Info("Binance repair task completed",
			"task_key", task.TaskKey, "market_id", task.MarketID,
			"interval", task.Interval, "candles", len(klines))
	}
}

func expectedRepairCandles(start, end time.Time, interval time.Duration) int {
	if interval <= 0 || !start.Before(end) {
		return 0
	}
	return int(end.Sub(start) / interval)
}

func repairRangeComplete(start, end time.Time, interval time.Duration, openTimes []time.Time) bool {
	expected := expectedRepairCandles(start, end, interval)
	if expected == 0 || len(openTimes) != expected {
		return false
	}
	seen := make(map[int64]struct{}, len(openTimes))
	for _, value := range openTimes {
		seen[value.UTC().UnixNano()] = struct{}{}
	}
	for cursor := start.UTC(); cursor.Before(end.UTC()); cursor = cursor.Add(interval) {
		if _, ok := seen[cursor.UnixNano()]; !ok {
			return false
		}
	}
	return true
}

func (b *BinanceTickerCrawler) providerMarketByID(marketID string) (database.ProviderMarket, bool) {
	for _, market := range b.markets {
		if market.MarketID == marketID {
			return market, true
		}
	}
	return database.ProviderMarket{}, false
}

func repairRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute
	for index := 1; index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// storeKlines 解析 Binance K 线并 upsert 落库（guid 主键 Save 即 upsert）。
// 返回本批次最大的 openTime（ms），没有有效数据时返回 0。
func (b *BinanceTickerCrawler) storeKlines(marketID, guid, interval string, klines [][]interface{}) int64 {
	var lastOpen int64
	for _, k := range klines {
		if len(k) < 6 {
			continue
		}

		openTime := parseKlineOpenTime(k[0])
		if openTime <= 0 {
			continue
		}

		open, err := decimalStringToUint256String(fmt.Sprintf("%v", k[1]), 8)
		if err != nil {
			log.Error("kline scale open failed", "guid", guid, "interval", interval, "err", err)
			continue
		}
		high, err := decimalStringToUint256String(fmt.Sprintf("%v", k[2]), 8)
		if err != nil {
			log.Error("kline scale high failed", "guid", guid, "interval", interval, "err", err)
			continue
		}
		low, err := decimalStringToUint256String(fmt.Sprintf("%v", k[3]), 8)
		if err != nil {
			log.Error("kline scale low failed", "guid", guid, "interval", interval, "err", err)
			continue
		}
		closePrice, err := decimalStringToUint256String(fmt.Sprintf("%v", k[4]), 8)
		if err != nil {
			log.Error("kline scale close failed", "guid", guid, "interval", interval, "err", err)
			continue
		}
		volume, err := decimalStringToUint256String(fmt.Sprintf("%v", k[5]), 8)
		if err != nil {
			log.Error("kline scale volume failed", "guid", guid, "interval", interval, "err", err)
			continue
		}

		kline := database.SymbolKline{
			Guid:       fmt.Sprintf("%s-%s-%d", guid, interval, openTime),
			MarketID:   marketID,
			SymbolGuid: guid,
			Interval:   interval,
			OpenTime:   time.UnixMilli(openTime),
			OpenPrice:  open,
			HighPrice:  high,
			LowPrice:   low,
			ClosePrice: closePrice,
			Volume:     volume,
			MarketCap:  "0",
			IsActive:   true,
			IngestedAt: time.Now(),
			CreatedAt:  time.UnixMilli(openTime),
			UpdatedAt:  time.Now(),
		}
		if err := b.db.SymbolKline.StoreSymbolKline(&kline); err != nil {
			log.Error("store kline failed", "guid", kline.Guid, "err", err)
			continue
		}
		if openTime > lastOpen {
			lastOpen = openTime
		}
	}
	return lastOpen
}

// parseKlineOpenTime 解析 Binance 返回的 openTime（数字或字符串）
func parseKlineOpenTime(raw interface{}) int64 {
	switch v := raw.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case string:
		ts, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return ts
		}
	}
	return 0
}

// klineOpenTimeMs 从落库行推导 openTime：优先取 guid 最后一个 "-" 后缀
// （兼容旧格式 "s1-<ms>" 和新格式 "s1-<interval>-<ms>"），失败退回 CreatedAt。
func klineOpenTimeMs(k *database.SymbolKline) int64 {
	idx := strings.LastIndex(k.Guid, "-")
	if idx >= 0 && idx+1 < len(k.Guid) {
		if ts, err := strconv.ParseInt(k.Guid[idx+1:], 10, 64); err == nil && ts > 0 {
			return ts
		}
	}
	return k.CreatedAt.UnixMilli()
}

func decimalStringToUint256String(value string, scale int64) (string, error) {
	val, ok := new(big.Float).SetString(value)
	if !ok {
		return "", fmt.Errorf("invalid decimal string: %s", value)
	}

	// multiplier = 10^scale
	multiplier := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil))

	// scaled = val * multiplier
	scaled := new(big.Float).Mul(val, multiplier)

	// to integer string (remove fractional part)
	result := new(big.Int)
	scaled.Int(result)

	return result.String(), nil
}
