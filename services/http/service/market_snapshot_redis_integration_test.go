package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/database"
	qredis "github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/model"
)

type snapshotFixtureSource struct {
	marker  string
	ready   chan<- struct{}
	release <-chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *snapshotFixtureSource) QueryMarketReadSnapshot(string) (*database.MarketReadSnapshot, error) {
	if s.ready != nil {
		s.ready <- struct{}{}
	}
	if s.release != nil {
		<-s.release
	}
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return frozenMarketFixture(s.marker), nil
}

func frozenMarketFixture(marker string) *database.MarketReadSnapshot {
	return frozenMarketFixtureRows(marker, 106)
}

func frozenMarketFixtureRows(marker string, total int) *database.MarketReadSnapshot {
	asOf := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	freshCount := min(total, 40)
	staleCount := min(total-freshCount, 21)
	unavailableCount := total - freshCount - staleCount
	rows := make([]database.AssetIndexDashboardRow, 0, total)
	for index := 0; index < total; index++ {
		freshness := "unavailable"
		if index < freshCount {
			freshness = "fresh"
		} else if index < freshCount+staleCount {
			freshness = "stale"
		}
		price := (*string)(nil)
		if freshness != "unavailable" {
			value := strconv.Itoa(10_000 + index)
			price = &value
		}
		rank := index + 1
		rows = append(rows, database.AssetIndexDashboardRow{
			AssetID: fmt.Sprintf("asset-%03d", index+1), AssetSymbol: fmt.Sprintf("A%03d", index+1),
			AssetName: marker, Rank: &rank, SelectionRank: rank, DisplayPrice: price,
			DisplayAvailable: freshness != "unavailable", FreshnessStatus: freshness,
		})
	}
	return &database.MarketReadSnapshot{
		AsOf: asOf,
		Summary: database.AssetIndexSummary{
			AssetCount: int64(total), PricedAssetCount: int64(freshCount + staleCount),
			DisplayedAssetCount: int64(freshCount + staleCount), UnpricedAssetCount: int64(unavailableCount),
			SingleVenuePricedAssetCount: int64(freshCount + staleCount),
		},
		Rows: rows, Total: int64(total), FreshAssetCount: int64(freshCount),
		StaleAssetCount: int64(staleCount), UnavailableAssetCount: int64(unavailableCount),
	}
}

func snapshotFixtureContract() MarketSnapshotContract {
	return MarketSnapshotContract{
		ReleaseCommit: "0123456789abcdef0123456789abcdef01234567",
		DataMode:      "live", ProviderPolicy: "restricted-no-bypass.v1",
		ContractSchema: "qiu.market-read-contract.v1", SnapshotSchema: MarketSnapshotSchema,
	}
}

func startIsolatedRedis(t *testing.T) (*qredis.Client, string) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is required for the real Redis integration test")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	dir := t.TempDir()
	command := exec.Command(binary,
		"--bind", "127.0.0.1", "--port", strconv.Itoa(port),
		"--protected-mode", "yes", "--save", "", "--appendonly", "no",
		"--dir", dir, "--pidfile", filepath.Join(dir, "redis.pid"),
		"--logfile", filepath.Join(dir, "redis.log"),
	)
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Signal(os.Interrupt)
		}
		_ = command.Wait()
	})
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var client *qredis.Client
	require.Eventually(t, func() bool {
		candidate, connectErr := qredis.New(qredis.Config{Address: address})
		if connectErr != nil {
			return false
		}
		client = candidate
		return true
	}, 5*time.Second, 25*time.Millisecond)
	t.Cleanup(func() { _ = client.Close() })
	return client, address
}

func TestMarketSnapshotRedisAuthorityAcrossInstances(t *testing.T) {
	client, _ := startIsolatedRedis(t)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	storeA := newMarketSnapshotStore(
		&snapshotFixtureSource{marker: "instance-a", ready: ready, release: release},
		client, snapshotFixtureContract(),
	)
	storeB := newMarketSnapshotStore(
		&snapshotFixtureSource{marker: "instance-b", ready: ready, release: release},
		client, snapshotFixtureContract(),
	)
	fixedNow := time.Date(2026, 8, 12, 2, 3, 4, 0, time.UTC)
	storeA.now = func() time.Time { return fixedNow }
	storeB.now = func() time.Time { return fixedNow }

	results := make(chan *marketSnapshot, 2)
	errorsSeen := make(chan error, 2)
	for _, store := range []*marketSnapshotStore{storeA, storeB} {
		go func(candidate *marketSnapshotStore) {
			result, resolveErr := candidate.resolve("all", "")
			results <- result
			errorsSeen <- resolveErr
		}(store)
	}
	<-ready
	<-ready
	close(release)
	require.NoError(t, <-errorsSeen)
	require.NoError(t, <-errorsSeen)
	first, second := <-results, <-results
	require.Equal(t, first.ID, second.ID)
	require.Len(t, first.Read.Rows, 106)
	require.Equal(t, first.Read.Rows[0].AssetName, second.Read.Rows[0].AssetName)
	require.EqualValues(t, 61, first.Read.Summary.PricedAssetCount)
	require.EqualValues(t, 45, first.Read.Summary.UnpricedAssetCount)

	readBack, err := storeB.resolve("all", first.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, readBack.ID)
	require.Equal(t, first.Contract.ReleaseCommit, readBack.Contract.ReleaseCommit)
}

func TestAssetDashboardCarriesOverviewFromTheSameRedisSnapshot(t *testing.T) {
	client, _ := startIsolatedRedis(t)
	store := newMarketSnapshotStore(
		&snapshotFixtureSource{marker: "atomic-dashboard"},
		client,
		snapshotFixtureContract(),
	)
	store.now = func() time.Time { return time.Date(2026, 8, 12, 2, 3, 4, 0, time.UTC) }
	handle := HandleSvc{marketSnapshots: store}
	response, err := handle.GetAssetDashboardV2(&model.AssetDashboardV2Request{
		Venue: "all", Universe: "provider_union", Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Len(t, response.Result, 50)
	require.EqualValues(t, 106, response.Total)
	require.EqualValues(t, 106, response.Overview.AssetCount)
	require.EqualValues(t, 40, response.Overview.FreshAssetCount)
	require.EqualValues(t, 21, response.Overview.StaleAssetCount)
	require.EqualValues(t, 45, response.Overview.UnavailableAssetCount)
	require.EqualValues(t, response.Overview.AssetCount,
		response.Overview.FreshAssetCount+response.Overview.StaleAssetCount+response.Overview.UnavailableAssetCount)
	require.Equal(t, store.bucketID("all", store.now().Truncate(marketSnapshotCurrentFor)), response.SnapshotID)
}

func TestMarketSnapshotCardinalityUsesBoundedTop200Union(t *testing.T) {
	store := newMarketSnapshotStore(
		&snapshotFixtureSource{marker: "cardinality"}, nil, snapshotFixtureContract(),
	)
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	entryFor := func(total int) *marketSnapshot {
		return &marketSnapshot{
			ID: "snp_0123456789abcdef0123456789abcdef", Venue: "all",
			CreatedAt: now, ExpiresAt: now.Add(marketSnapshotTTL), Schema: MarketSnapshotSchema,
			Contract: snapshotFixtureContract(), Read: frozenMarketFixtureRows("cardinality", total),
		}
	}

	for _, total := range []int{106, 109, marketSnapshotMaximumRows} {
		t.Run(fmt.Sprintf("accepts_%d", total), func(t *testing.T) {
			require.NoError(t, store.validate(entryFor(total), "all", now))
		})
	}
	for _, total := range []int{0, marketSnapshotMaximumRows + 1} {
		t.Run(fmt.Sprintf("rejects_%d", total), func(t *testing.T) {
			require.ErrorIs(t, store.validate(entryFor(total), "all", now), ErrMarketSnapshotInvalid)
		})
	}
}

func TestMarketSnapshotRedisFailsClosedForExpiryCorruptionAndOutage(t *testing.T) {
	client, _ := startIsolatedRedis(t)
	store := newMarketSnapshotStore(&snapshotFixtureSource{marker: "valid"}, client, snapshotFixtureContract())
	fixedNow := time.Date(2026, 8, 12, 3, 4, 5, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }
	snapshot, err := store.resolve("all", "")
	require.NoError(t, err)

	store.now = func() time.Time { return fixedNow.Add(marketSnapshotTTL + time.Second) }
	_, err = store.resolve("all", snapshot.ID)
	require.ErrorIs(t, err, ErrMarketSnapshotExpired)
	store.now = func() time.Time { return fixedNow }

	corruptID := "snp_00000000000000000000000000000000"
	require.NoError(t, client.Set(context.Background(), store.snapshotKey(corruptID), "{", time.Minute))
	_, err = store.resolve("all", corruptID)
	require.ErrorIs(t, err, ErrMarketSnapshotInvalid)

	for _, test := range []struct {
		name   string
		id     string
		tamper func(*database.MarketReadSnapshot)
	}{
		{
			name: "row freshness does not match self-reported counts",
			id:   "snp_11111111111111111111111111111111",
			tamper: func(read *database.MarketReadSnapshot) {
				read.Rows[0].FreshnessStatus = "unavailable"
			},
		},
		{
			name: "duplicate asset id",
			id:   "snp_22222222222222222222222222222222",
			tamper: func(read *database.MarketReadSnapshot) {
				read.Rows[1].AssetID = read.Rows[0].AssetID
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			read := frozenMarketFixture("tampered")
			test.tamper(read)
			entry := &marketSnapshot{
				ID: test.id, Venue: "all", CreatedAt: fixedNow,
				ExpiresAt: fixedNow.Add(marketSnapshotTTL), Schema: MarketSnapshotSchema,
				Contract: snapshotFixtureContract(), Read: read,
			}
			payload, marshalErr := json.Marshal(entry)
			require.NoError(t, marshalErr)
			require.NoError(t, client.Set(context.Background(), store.snapshotKey(test.id), payload, time.Minute))
			_, resolveErr := store.resolve("all", test.id)
			require.ErrorIs(t, resolveErr, ErrMarketSnapshotInvalid)
		})
	}

	require.NoError(t, client.Close())
	_, err = store.resolve("all", snapshot.ID)
	require.ErrorIs(t, err, ErrMarketSnapshotUnavailable)
}

func TestMarketSnapshotRedisEvictsBeyondBound(t *testing.T) {
	client, _ := startIsolatedRedis(t)
	store := newMarketSnapshotStore(&snapshotFixtureSource{marker: "bounded"}, client, snapshotFixtureContract())
	start := time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)
	ids := make([]string, 0, marketSnapshotMaximumItems+1)
	for index := 0; index <= marketSnapshotMaximumItems; index++ {
		instant := start.Add(time.Duration(index) * marketSnapshotCurrentFor)
		store.now = func() time.Time { return instant }
		snapshot, err := store.resolve("all", "")
		require.NoError(t, err)
		ids = append(ids, snapshot.ID)
	}
	store.now = func() time.Time { return start.Add(time.Minute) }
	_, err := store.resolve("all", ids[0])
	require.ErrorIs(t, err, ErrMarketSnapshotExpired)
	latest, err := store.resolve("all", ids[len(ids)-1])
	require.NoError(t, err)
	require.Equal(t, ids[len(ids)-1], latest.ID)
}

func TestRedisUnlockIfValuePreservesNewOwner(t *testing.T) {
	client, _ := startIsolatedRedis(t)
	ctx := context.Background()
	locked, err := client.TryLock(ctx, "qiu:test:lease", "owner-a", time.Minute)
	require.NoError(t, err)
	require.True(t, locked)
	unlocked, err := client.UnlockIfValue(ctx, "qiu:test:lease", "owner-b")
	require.NoError(t, err)
	require.False(t, unlocked)
	value, err := client.Get(ctx, "qiu:test:lease")
	require.NoError(t, err)
	require.Equal(t, "owner-a", value)
	unlocked, err = client.UnlockIfValue(ctx, "qiu:test:lease", "owner-a")
	require.NoError(t, err)
	require.True(t, unlocked)
	_, err = client.Get(ctx, "qiu:test:lease")
	require.True(t, qredis.IsNotFound(err))
	require.False(t, errors.Is(err, context.Canceled))
}

func TestSnapshotDashboardPagePreservesPublishedBoundary(t *testing.T) {
	snapshot := &marketSnapshot{Read: &database.MarketReadSnapshot{Rows: []database.AssetIndexDashboardRow{
		{AssetID: "published", AssetSymbol: "PUB", Published: true, FreshnessStatus: "fresh"},
		{AssetID: "rollout-pending", AssetSymbol: "PEND", Published: false, FreshnessStatus: "unavailable"},
	}}}
	rows, total := snapshotDashboardPage(snapshot, database.AssetIndexDashboardQuery{
		Page: 1, PageSize: 50, Venue: "bybit", IncludeUncovered: false,
	})
	require.EqualValues(t, 1, total)
	require.Equal(t, "published", rows[0].AssetID)
	rows, total = snapshotDashboardPage(snapshot, database.AssetIndexDashboardQuery{
		Page: 1, PageSize: 50, Venue: "bybit", IncludeUncovered: true,
	})
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 2)
}
