package marketdata

import (
	"context"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/database"
)

const snapshotCacheTTL = 10 * time.Minute

type SnapshotStore interface {
	ApplyMarketSnapshot(database.MarketSnapshotInput) (database.MarketSnapshotResult, error)
}

type SnapshotCache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	ZAdd(ctx context.Context, key string, score float64, member any) error
	ZRem(ctx context.Context, key string, members ...any) error
}

type Snapshot struct {
	database.MarketSnapshotInput
	ExchangeGuid string
	ExchangeName string
	SymbolName   string
}

// SnapshotWriter is the only adapter-facing write path for current market
// snapshots. PostgreSQL owns ordering/correction decisions; Redis is a derived
// cache refreshed only after the database accepts the observation.
type SnapshotWriter struct {
	store SnapshotStore
	cache SnapshotCache
}

func NewSnapshotWriter(store SnapshotStore, cache SnapshotCache) *SnapshotWriter {
	return &SnapshotWriter{store: store, cache: cache}
}

func (w *SnapshotWriter) Write(ctx context.Context, snapshot Snapshot) (database.MarketSnapshotResult, error) {
	result, err := w.store.ApplyMarketSnapshot(snapshot.MarketSnapshotInput)
	if err != nil {
		return database.MarketSnapshotResult{}, err
	}
	if result.Action == database.MarketSnapshotDiscarded {
		return result, nil
	}
	if w.cache == nil {
		return result, nil
	}

	cacheCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	key := marketkey.Build(
		snapshot.ExchangeGuid,
		snapshot.ExchangeName,
		snapshot.SymbolGuid,
		snapshot.SymbolName,
	)
	ttl := snapshotCacheTTL + time.Duration(rand.Int64N(60))*time.Second
	cacheWrites := []struct {
		suffix string
		value  string
	}{
		{"", snapshot.Price},
		{"askPrice", snapshot.AskPrice},
		{"bidPrice", snapshot.BidPrice},
		{"volume", snapshot.Volume},
	}
	for _, write := range cacheWrites {
		if err := w.cache.Set(cacheCtx, key+write.suffix, write.value, ttl); err != nil {
			log.Warn("snapshot cache refresh failed",
				"market_id", snapshot.MarketID, "field", write.suffix, "error", err)
		}
	}

	if snapshot.Change24hPct == nil {
		if err := w.cache.ZRem(cacheCtx, marketkey.RankChange24hKey, snapshot.SymbolGuid); err != nil {
			log.Warn("snapshot rank remove failed", "market_id", snapshot.MarketID, "error", err)
		}
		return result, nil
	}
	change, err := strconv.ParseFloat(*snapshot.Change24hPct, 64)
	if err != nil {
		log.Warn("snapshot rank skipped for invalid change percentage",
			"market_id", snapshot.MarketID, "value", *snapshot.Change24hPct)
		return result, nil
	}
	if err := w.cache.ZAdd(cacheCtx, marketkey.RankChange24hKey, change, snapshot.SymbolGuid); err != nil {
		log.Warn("snapshot rank refresh failed", "market_id", snapshot.MarketID, "error", err)
	}
	return result, nil
}
