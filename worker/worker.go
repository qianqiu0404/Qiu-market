package worker

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/common/markettime"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Worker struct {
	db      *database.DB
	cancel  context.CancelFunc
	stopped atomic.Bool
}

func NewWorker(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Worker, error) {
	return &Worker{db: db}, nil
}

func (w *Worker) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.runGapScanner(runCtx)
	log.Info("Starting worker (K-line gap scanner only; no market snapshot writes)")
	return nil
}

func (w *Worker) Stop(ctx context.Context) error {
	log.Info("Stopping worker")
	if w.cancel != nil {
		w.cancel()
	}
	w.stopped.Store(true)
	return nil
}

func (w *Worker) Stopped() bool {
	return w.stopped.Load()
}

var repairLookbacks = map[string]time.Duration{
	"1m":  6 * time.Hour,
	"15m": 48 * time.Hour,
	"1h":  7 * 24 * time.Hour,
	"1d":  30 * 24 * time.Hour,
}

func (w *Worker) runGapScanner(ctx context.Context) {
	w.scanKlineGaps(time.Now().UTC())
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			w.scanKlineGaps(now.UTC())
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) scanKlineGaps(now time.Time) {
	var tasks []database.KlineRepairTask
	var marketCount int
	for _, provider := range []string{"binance", "coinbase", "bybit", "okx"} {
		markets, err := w.db.ExchangeSymbol.QueryProviderKlineMarkets(provider)
		if err != nil {
			log.Error("worker query provider repair catalog failed",
				"provider", provider, "error", err)
			continue
		}
		marketCount += len(markets)
		for _, market := range markets {
			if !strings.EqualFold(market.MarketType, "spot") ||
				strings.TrimSpace(market.SourceSymbol) == "" {
				continue
			}
			for interval, lookback := range repairLookbacks {
				duration, err := markettime.Duration(interval)
				if err != nil {
					continue
				}
				end := now.Truncate(duration)
				start := end.Add(-lookback)
				openTimes, err := w.db.KlineRepair.QueryKlineOpenTimes(
					market.MarketID, interval, start, end,
				)
				if err != nil {
					log.Error("worker query K-line gap window failed",
						"provider", provider, "market_id", market.MarketID,
						"interval", interval, "error", err)
					continue
				}
				for _, gap := range FindKlineGaps(start, end, duration, openTimes) {
					tasks = append(tasks, database.KlineRepairTask{
						Provider:      provider,
						MarketID:      market.MarketID,
						SourceSymbol:  market.SourceSymbol,
						Interval:      interval,
						GapStart:      gap.Start,
						GapEnd:        gap.End,
						NextAttemptAt: now,
					})
				}
			}
		}
	}
	inserted, err := w.db.KlineRepair.UpsertRepairTasks(tasks)
	if err != nil {
		log.Error("worker persist K-line repair tasks failed", "error", err)
		return
	}
	log.Info("worker K-line gap scan complete",
		"markets", marketCount, "detected_ranges", len(tasks), "new_tasks", inserted)
}

type KlineGap struct {
	Start time.Time
	End   time.Time
}

// FindKlineGaps returns maximal contiguous missing ranges in [start, end).
// A range end is exclusive, matching Binance's repair request boundary.
func FindKlineGaps(start, end time.Time, interval time.Duration, openTimes []time.Time) []KlineGap {
	if interval <= 0 || !start.Before(end) {
		return nil
	}
	existing := make(map[int64]struct{}, len(openTimes))
	for _, value := range openTimes {
		existing[value.UTC().UnixNano()] = struct{}{}
	}
	var gaps []KlineGap
	var active *KlineGap
	for cursor := start.UTC(); cursor.Before(end.UTC()); cursor = cursor.Add(interval) {
		_, ok := existing[cursor.UnixNano()]
		if ok {
			if active != nil {
				gaps = append(gaps, *active)
				active = nil
			}
			continue
		}
		if active == nil {
			active = &KlineGap{Start: cursor, End: cursor.Add(interval)}
		} else {
			active.End = cursor.Add(interval)
		}
	}
	if active != nil {
		gaps = append(gaps, *active)
	}
	return gaps
}

func validateRepairTask(task database.KlineRepairTask) error {
	if strings.TrimSpace(task.SourceSymbol) == "" {
		return fmt.Errorf("repair source_symbol is empty")
	}
	if _, err := markettime.Duration(task.Interval); err != nil {
		return err
	}
	if !task.GapStart.Before(task.GapEnd) {
		return fmt.Errorf("repair range is empty")
	}
	return nil
}
