package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/database"
)

type snapshotStoreStub struct {
	input  database.MarketSnapshotInput
	result database.MarketSnapshotResult
	err    error
}

func (s *snapshotStoreStub) ApplyMarketSnapshot(input database.MarketSnapshotInput) (database.MarketSnapshotResult, error) {
	s.input = input
	return s.result, s.err
}

type snapshotCacheStub struct {
	values map[string]string
	scores map[string]float64
}

func (s *snapshotCacheStub) Set(_ context.Context, key string, value any, _ time.Duration) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value.(string)
	return nil
}

func (s *snapshotCacheStub) ZAdd(_ context.Context, key string, score float64, member any) error {
	if s.scores == nil {
		s.scores = map[string]float64{}
	}
	s.scores[key+"|"+member.(string)] = score
	return nil
}

func (s *snapshotCacheStub) ZRem(_ context.Context, key string, members ...any) error {
	for _, member := range members {
		delete(s.scores, key+"|"+member.(string))
	}
	return nil
}

func TestSnapshotWriterPersistsBeforeRefreshingDerivedCache(t *testing.T) {
	change := "2.625"
	store := &snapshotStoreStub{
		result: database.MarketSnapshotResult{Action: database.MarketSnapshotUpdated},
	}
	cache := &snapshotCacheStub{}
	writer := NewSnapshotWriter(store, cache)
	snapshot := Snapshot{
		MarketSnapshotInput: database.MarketSnapshotInput{
			MarketID:     "m1",
			SymbolGuid:   "s1",
			Price:        "6500000000000",
			AskPrice:     "6500100000000",
			BidPrice:     "6499900000000",
			Volume:       "100000000",
			Change24hPct: &change,
			IsActive:     true,
			ObservedAt:   time.Now(),
		},
		ExchangeGuid: "e1",
		ExchangeName: "Binance",
		SymbolName:   "BTC/USDT",
	}

	result, err := writer.Write(context.Background(), snapshot)
	require.NoError(t, err)
	require.Equal(t, database.MarketSnapshotUpdated, result.Action)
	require.Equal(t, "m1", store.input.MarketID)
	key := marketkey.Build("e1", "Binance", "s1", "BTC/USDT")
	require.Equal(t, snapshot.Price, cache.values[key])
	require.Equal(t, 2.625, cache.scores[marketkey.RankChange24hKey+"|s1"])
}

func TestSnapshotWriterDoesNotRefreshDiscardedObservation(t *testing.T) {
	store := &snapshotStoreStub{
		result: database.MarketSnapshotResult{Action: database.MarketSnapshotDiscarded},
	}
	cache := &snapshotCacheStub{}
	writer := NewSnapshotWriter(store, cache)

	_, err := writer.Write(context.Background(), Snapshot{
		MarketSnapshotInput: database.MarketSnapshotInput{
			MarketID:   "m1",
			SymbolGuid: "s1",
			ObservedAt: time.Now(),
		},
	})
	require.NoError(t, err)
	require.Empty(t, cache.values)
}
