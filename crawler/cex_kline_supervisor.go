package crawler

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/common/markettime"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
)

const (
	cexKlineInitialLookback = 24 * time.Hour
	cexKlineRefreshInterval = 30 * time.Second
	cexKlineRequestSpacing  = 120 * time.Millisecond
)

var derivedKlineIntervals = []string{"15m", "1h", "1d"}

type CEXKlineSupervisor struct {
	db       *database.DB
	reporter *marketdata.ProviderReporter
	adapters []cexKlineAdapter
	cancel   context.CancelFunc
	stopped  atomic.Bool
}

func NewCEXKlineSupervisor(db *database.DB) *CEXKlineSupervisor {
	client := newKlineHTTPClient()
	return &CEXKlineSupervisor{
		db:       db,
		reporter: marketdata.NewProviderReporter(db.ProviderStatus),
		adapters: []cexKlineAdapter{
			&binanceKlineAdapter{baseCEXKlineAdapter{
				client: client, baseURL: "https://api.binance.com",
			}},
			&coinbaseKlineAdapter{baseCEXKlineAdapter{
				client: client, baseURL: "https://api.exchange.coinbase.com",
			}},
			&bybitKlineAdapter{baseCEXKlineAdapter{
				client: client, baseURL: "https://api.bybit.com",
			}},
			&okxKlineAdapter{baseCEXKlineAdapter{
				client: client, baseURL: "https://www.okx.com",
			}},
		},
	}
}

func newKlineHTTPClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second}
}

func (s *CEXKlineSupervisor) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	for _, adapter := range s.adapters {
		adapter := adapter
		go s.superviseProvider(ctx, adapter)
	}
	log.Info("CEX K-line supervisor started",
		"providers", len(s.adapters),
		"source_interval", "1m",
		"derived_intervals", strings.Join(derivedKlineIntervals, ","))
	return nil
}

func (s *CEXKlineSupervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.stopped.Store(true)
}

func (s *CEXKlineSupervisor) Stopped() bool { return s.stopped.Load() }

func (s *CEXKlineSupervisor) superviseProvider(
	ctx context.Context,
	adapter cexKlineAdapter,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("CEX K-line provider panic isolated",
				"provider", adapter.Provider(),
				"panic", recovered,
				"stack", string(debug.Stack()))
			if ctx.Err() == nil {
				go s.superviseProvider(ctx, adapter)
			}
		}
	}()
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		markets, err := s.db.ExchangeSymbol.ReconcileProviderKlineSelection(adapter.Provider())
		if err != nil {
			s.reporter.Failure(adapter.Provider(), "klines", time.Now().UTC(), err, 0)
			s.reporter.NextRetry(adapter.Provider(), "klines", time.Now().UTC().Add(backoff))
			log.Warn("CEX K-line selection not ready",
				"provider", adapter.Provider(), "error", err, "retry_in", backoff)
			if !waitContext(ctx, backoff) {
				return
			}
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
			continue
		}
		backoff = 5 * time.Second
		s.syncProvider(ctx, adapter, markets)
		ticker := time.NewTicker(cexKlineRefreshInterval)
		reconcileTicker := time.NewTicker(5 * time.Minute)
		for ctx.Err() == nil {
			select {
			case <-ticker.C:
				s.syncProvider(ctx, adapter, markets)
				s.processRepairTasks(ctx, adapter, markets)
			case <-reconcileTicker.C:
				updated, reconcileErr := s.db.ExchangeSymbol.
					ReconcileProviderKlineSelection(adapter.Provider())
				if reconcileErr != nil {
					log.Warn("CEX K-line selection reconcile failed",
						"provider", adapter.Provider(), "error", reconcileErr)
					continue
				}
				markets = updated
			case <-ctx.Done():
				ticker.Stop()
				reconcileTicker.Stop()
				return
			}
		}
		ticker.Stop()
		reconcileTicker.Stop()
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *CEXKlineSupervisor) syncProvider(
	ctx context.Context,
	adapter cexKlineAdapter,
	markets []database.ProviderMarket,
) {
	if ctx.Err() != nil || len(markets) == 0 {
		return
	}
	provider := adapter.Provider()
	now := time.Now().UTC()
	s.reporter.Attempt(provider, "klines", now)
	var received, written, successfulMarkets int64
	var latestSource *time.Time
	var failures []string
	for _, market := range markets {
		if ctx.Err() != nil {
			return
		}
		count, last, err := s.syncMarket(ctx, adapter, market, now)
		if err != nil {
			failures = append(failures, market.SourceSymbol+": "+err.Error())
		} else {
			successfulMarkets++
			received += int64(count)
			written += int64(count)
			if last != nil && (latestSource == nil || last.After(*latestSource)) {
				value := *last
				latestSource = &value
			}
		}
		if !waitContext(ctx, cexKlineRequestSpacing) {
			return
		}
	}
	if successfulMarkets == 0 {
		err := fmt.Errorf("%s K-line cycle failed for all %d markets", provider, len(markets))
		if len(failures) > 0 {
			err = fmt.Errorf("%w: %s", err, strings.Join(failures[:minInt(3, len(failures))], "; "))
		}
		s.reporter.Failure(provider, "klines", time.Now().UTC(), err, 0)
		return
	}
	s.reporter.SuccessWithDetails(
		provider, "klines", time.Now().UTC(), latestSource,
		database.ProviderStatusDetails{
			ReceivedCount:     received,
			MatchedAssetCount: successfulMarkets,
			WrittenCount:      written,
			ProbeObservedAt:   time.Now().UTC().UnixMilli(),
		},
	)
	if len(failures) > 0 {
		log.Warn("CEX K-line cycle partially degraded",
			"provider", provider,
			"successful_markets", successfulMarkets,
			"failed_markets", len(failures),
			"sample", strings.Join(failures[:minInt(3, len(failures))], "; "))
	} else {
		log.Info("CEX K-line cycle complete",
			"provider", provider, "markets", successfulMarkets, "candles", received)
	}
}

func (s *CEXKlineSupervisor) syncMarket(
	ctx context.Context,
	adapter cexKlineAdapter,
	market database.ProviderMarket,
	now time.Time,
) (int, *time.Time, error) {
	latest, err := s.db.SymbolKline.QueryLatestMarketKline(market.MarketID, "1m")
	if err != nil {
		return 0, nil, err
	}
	end := now.UTC().Truncate(time.Minute).Add(time.Minute)
	start := end.Add(-cexKlineInitialLookback)
	if latest != nil {
		start = latest.OpenTime.UTC().Add(-2 * time.Minute)
		minimum := end.Add(-cexKlineInitialLookback)
		if start.Before(minimum) {
			start = minimum
		}
	}
	count, latestOpen, err := s.fetchStoreRange(ctx, adapter, market, start, end)
	if err != nil {
		return count, latestOpen, err
	}
	return count, latestOpen, nil
}

func (s *CEXKlineSupervisor) fetchStoreRange(
	ctx context.Context,
	adapter cexKlineAdapter,
	market database.ProviderMarket,
	start, end time.Time,
) (int, *time.Time, error) {
	start = start.UTC().Truncate(time.Minute)
	end = end.UTC().Truncate(time.Minute)
	if !start.Before(end) {
		return 0, nil, nil
	}
	pageLimit := adapter.PageLimit()
	if pageLimit < 1 {
		return 0, nil, fmt.Errorf("provider %s has invalid K-line page limit", adapter.Provider())
	}
	var total int
	var latest *time.Time
	for cursor := start; cursor.Before(end); {
		pageEnd := cursor.Add(time.Duration(pageLimit) * time.Minute)
		if pageEnd.After(end) {
			pageEnd = end
		}
		rows, err := adapter.Fetch1m(
			ctx, market.SourceSymbol, cursor, pageEnd, pageLimit,
		)
		if err != nil {
			return total, latest, err
		}
		if err := s.storeNormalizedKlines(market, rows); err != nil {
			return total, latest, err
		}
		if err := s.aggregateAffectedBuckets(market, rows, time.Now().UTC()); err != nil {
			return total, latest, err
		}
		total += len(rows)
		if len(rows) > 0 {
			value := rows[len(rows)-1].OpenTime
			latest = &value
		}
		cursor = pageEnd
		if cursor.Before(end) && !waitContext(ctx, cexKlineRequestSpacing) {
			return total, latest, ctx.Err()
		}
	}
	return total, latest, nil
}

func (s *CEXKlineSupervisor) storeNormalizedKlines(
	market database.ProviderMarket,
	rows []normalizedCEXKline,
) error {
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UTC()
	values := make([]database.SymbolKline, 0, len(rows))
	for _, row := range rows {
		open, err := decimalStringToUint256String(row.Open, 8)
		if err != nil {
			return err
		}
		high, err := decimalStringToUint256String(row.High, 8)
		if err != nil {
			return err
		}
		low, err := decimalStringToUint256String(row.Low, 8)
		if err != nil {
			return err
		}
		closePrice, err := decimalStringToUint256String(row.Close, 8)
		if err != nil {
			return err
		}
		volume, err := decimalStringToUint256String(row.Volume, 8)
		if err != nil {
			return err
		}
		openTime := row.OpenTime.UTC().Truncate(time.Minute)
		values = append(values, database.SymbolKline{
			Guid:     fmt.Sprintf("%s-1m-%d", market.SymbolGuid, openTime.UnixMilli()),
			MarketID: market.MarketID, SymbolGuid: market.SymbolGuid,
			Interval: "1m", OpenTime: openTime,
			OpenPrice: open, HighPrice: high, LowPrice: low,
			ClosePrice: closePrice, Volume: volume, MarketCap: "0",
			IsActive: true, IngestedAt: now,
			CreatedAt: openTime, UpdatedAt: now,
		})
	}
	return s.db.SymbolKline.StoreSymbolKlines(values)
}

func (s *CEXKlineSupervisor) aggregateAffectedBuckets(
	market database.ProviderMarket,
	oneMinuteRows []normalizedCEXKline,
	now time.Time,
) error {
	if len(oneMinuteRows) == 0 {
		return nil
	}
	for _, interval := range derivedKlineIntervals {
		duration, err := markettime.Duration(interval)
		if err != nil {
			return err
		}
		buckets := make(map[int64]time.Time)
		for _, row := range oneMinuteRows {
			bucket := row.OpenTime.UTC().Truncate(duration)
			buckets[bucket.UnixMilli()] = bucket
		}
		keys := make([]int64, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		aggregated := make([]database.SymbolKline, 0, len(keys))
		for _, key := range keys {
			start := buckets[key]
			end := start.Add(duration)
			rows, queryErr := s.db.SymbolKline.QueryMarketKlinesBetween(
				market.MarketID, "1m", start, end,
			)
			if queryErr != nil {
				return queryErr
			}
			value, complete, aggregateErr := aggregateOneMinuteBucket(
				market, interval, start, rows, now,
			)
			if aggregateErr != nil {
				return aggregateErr
			}
			if complete {
				aggregated = append(aggregated, value)
			}
		}
		if err := s.db.SymbolKline.StoreSymbolKlines(aggregated); err != nil {
			return err
		}
	}
	return nil
}

func aggregateOneMinuteBucket(
	market database.ProviderMarket,
	interval string,
	bucketStart time.Time,
	rows []database.SymbolKline,
	now time.Time,
) (database.SymbolKline, bool, error) {
	duration, err := markettime.Duration(interval)
	if err != nil {
		return database.SymbolKline{}, false, err
	}
	currentMinute := now.UTC().Truncate(time.Minute)
	bucketEnd := bucketStart.Add(duration)
	expectedEnd := bucketEnd
	if expectedEnd.After(currentMinute.Add(time.Minute)) {
		expectedEnd = currentMinute.Add(time.Minute)
	}
	expected := int(expectedEnd.Sub(bucketStart) / time.Minute)
	if expected <= 0 || len(rows) != expected {
		return database.SymbolKline{}, false, nil
	}
	for index, row := range rows {
		expectedOpen := bucketStart.Add(time.Duration(index) * time.Minute)
		if !row.OpenTime.UTC().Equal(expectedOpen) {
			return database.SymbolKline{}, false, nil
		}
	}
	open, err := scaledInteger(rows[0].OpenPrice)
	if err != nil {
		return database.SymbolKline{}, false, fmt.Errorf(
			"invalid open price %q: %w", rows[0].OpenPrice, err,
		)
	}
	closePrice, err := scaledInteger(rows[len(rows)-1].ClosePrice)
	if err != nil {
		return database.SymbolKline{}, false, fmt.Errorf(
			"invalid close price %q: %w", rows[len(rows)-1].ClosePrice, err,
		)
	}
	high, err := scaledInteger(rows[0].HighPrice)
	if err != nil {
		return database.SymbolKline{}, false, fmt.Errorf(
			"invalid high price %q: %w", rows[0].HighPrice, err,
		)
	}
	low, err := scaledInteger(rows[0].LowPrice)
	if err != nil {
		return database.SymbolKline{}, false, fmt.Errorf(
			"invalid low price %q: %w", rows[0].LowPrice, err,
		)
	}
	volume := new(big.Int)
	for _, row := range rows {
		value, parseErr := scaledInteger(row.HighPrice)
		if parseErr != nil {
			return database.SymbolKline{}, false, fmt.Errorf(
				"invalid high price %q: %w", row.HighPrice, parseErr,
			)
		}
		if value.Cmp(high) > 0 {
			high.Set(value)
		}
		value, parseErr = scaledInteger(row.LowPrice)
		if parseErr != nil {
			return database.SymbolKline{}, false, fmt.Errorf(
				"invalid low price %q: %w", row.LowPrice, parseErr,
			)
		}
		if value.Cmp(low) < 0 {
			low.Set(value)
		}
		value, parseErr = scaledInteger(row.Volume)
		if parseErr != nil {
			return database.SymbolKline{}, false, fmt.Errorf(
				"invalid volume %q: %w", row.Volume, parseErr,
			)
		}
		volume.Add(volume, value)
	}
	recordedAt := time.Now().UTC()
	return database.SymbolKline{
		Guid: fmt.Sprintf(
			"%s-%s-%d", market.SymbolGuid, interval, bucketStart.UnixMilli(),
		),
		MarketID: market.MarketID, SymbolGuid: market.SymbolGuid,
		Interval: interval, OpenTime: bucketStart,
		OpenPrice: open.String(), HighPrice: high.String(),
		LowPrice: low.String(), ClosePrice: closePrice.String(),
		Volume: volume.String(), MarketCap: "0", IsActive: true,
		IngestedAt: recordedAt, CreatedAt: bucketStart, UpdatedAt: recordedAt,
	}, true, nil
}

// scaledInteger accepts PostgreSQL NUMERIC values such as "123.0000" while
// still rejecting any non-zero fractional component. K-line prices and volume
// are already scaled to integers before storage, but database drivers preserve
// the column's declared scale when scanning them back into strings.
func scaledInteger(value string) (*big.Int, error) {
	parsed, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return nil, err
	}
	if !parsed.Equal(parsed.Truncate(0)) {
		return nil, fmt.Errorf("scaled value has a fractional component")
	}
	result := new(big.Int)
	if _, ok := result.SetString(parsed.StringFixed(0), 10); !ok {
		return nil, fmt.Errorf("scaled value is outside integer representation")
	}
	return result, nil
}

func (s *CEXKlineSupervisor) processRepairTasks(
	ctx context.Context,
	adapter cexKlineAdapter,
	markets []database.ProviderMarket,
) {
	now := time.Now().UTC()
	tasks, err := s.db.KlineRepair.ClaimRepairTasks(adapter.Provider(), 5, now)
	if err != nil {
		log.Warn("CEX repair task claim failed",
			"provider", adapter.Provider(), "error", err)
		return
	}
	marketByID := make(map[string]database.ProviderMarket, len(markets))
	for _, market := range markets {
		marketByID[market.MarketID] = market
	}
	for _, task := range tasks {
		market, ok := marketByID[task.MarketID]
		if !ok {
			s.retryRepairTask(task, fmt.Errorf(
				"market is absent from active provider K-line selection",
			))
			continue
		}
		if _, _, err := s.fetchStoreRange(
			ctx, adapter, market, task.GapStart, task.GapEnd,
		); err != nil {
			s.retryRepairTask(task, err)
			continue
		}
		duration, durationErr := markettime.Duration(task.Interval)
		openTimes, queryErr := s.db.KlineRepair.QueryKlineOpenTimes(
			task.MarketID, task.Interval, task.GapStart, task.GapEnd,
		)
		if durationErr != nil || queryErr != nil ||
			!repairRangeComplete(task.GapStart, task.GapEnd, duration, openTimes) {
			verifyErr := durationErr
			if verifyErr == nil {
				verifyErr = queryErr
			}
			if verifyErr == nil {
				verifyErr = fmt.Errorf(
					"repair range remains incomplete: expected=%d actual=%d",
					expectedRepairCandles(task.GapStart, task.GapEnd, duration),
					len(openTimes),
				)
			}
			s.retryRepairTask(task, verifyErr)
			continue
		}
		if err := s.db.KlineRepair.CompleteRepairTask(
			task.TaskKey, time.Now().UTC(),
		); err != nil {
			log.Warn("CEX repair task completion failed",
				"task_key", task.TaskKey, "error", err)
		}
	}
}

func (s *CEXKlineSupervisor) retryRepairTask(
	task database.KlineRepairTask,
	err error,
) {
	permanent := task.AttemptCount >= 8
	retryAt := time.Now().UTC().Add(repairRetryDelay(task.AttemptCount))
	if writeErr := s.db.KlineRepair.RetryRepairTask(
		task.TaskKey, err.Error(), retryAt, permanent,
	); writeErr != nil {
		log.Warn("CEX repair task retry update failed",
			"task_key", task.TaskKey, "error", writeErr)
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
