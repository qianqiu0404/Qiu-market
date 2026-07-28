package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

var errCommitOutcomeUnknown = errors.New("commit outcome unknown")

type uncertainStore struct {
	*store.Memory
	mu       sync.Mutex
	failNext bool
}

func (s *uncertainStore) Append(ctx context.Context, expectedSequence uint64, record store.Record) error {
	if err := s.Memory.Append(ctx, expectedSequence, record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		s.failNext = false
		return errCommitOutcomeUnknown
	}
	return nil
}

type blockingStore struct {
	*store.Memory
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStore) Append(ctx context.Context, expectedSequence uint64, record store.Record) error {
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return s.Memory.Append(ctx, expectedSequence, record)
}

type deadlineSnapshotStore struct {
	*store.Memory
	deadlines chan time.Duration
}

func (s *deadlineSnapshotStore) Save(ctx context.Context, snapshot store.Snapshot) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("snapshot context has no deadline")
	}
	s.deadlines <- time.Until(deadline)
	return s.Memory.Save(ctx, snapshot)
}

func TestMarketRunnerRecoversUnknownCommitAndRetryIsIdempotent(t *testing.T) {
	t.Parallel()

	persistence := &uncertainStore{Memory: store.NewMemory(), failNext: true}
	runner, err := runtime.NewMarketRunner(
		context.Background(),
		testMarket(),
		persistence,
		persistence,
		runtime.Config{QueueSize: 8, SnapshotEvery: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRunner(t, runner)

	request := domain.FundRequest{
		RequestID: "fund-after-unknown-commit",
		AccountID: "alice",
		Asset:     "USDT",
		Amount:    1_000,
	}
	if _, err := runner.Fund(context.Background(), request); !errors.Is(err, errCommitOutcomeUnknown) ||
		!errors.Is(err, exchange.ErrPersistence) {
		t.Fatalf("unknown commit error = %v", err)
	}
	status := runner.Status()
	if status.State != runtime.StateReady || status.Sequence != 1 || status.RecoveryCount != 1 {
		t.Fatalf("runner status after recovery = %+v", status)
	}
	if status.LastError != "" ||
		status.LastIncident == "" ||
		status.LastIncidentAt == "" ||
		status.LastRecoveredAt == "" {
		t.Fatalf("runner incident status after successful recovery = %+v", status)
	}

	result, err := runner.Fund(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sequence != 1 || runner.Status().Sequence != 1 {
		t.Fatalf("idempotent retry sequence = result %d runner %d", result.Sequence, runner.Status().Sequence)
	}
	balance, err := runner.Balance("alice", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if balance.Available != 1_000 || balance.Held != 0 {
		t.Fatalf("restored balance = %+v", balance)
	}
}

func TestMarketRunnerBackpressureAndGracefulSnapshot(t *testing.T) {
	t.Parallel()

	persistence := &blockingStore{
		Memory:  store.NewMemory(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner, err := runtime.NewMarketRunner(
		context.Background(),
		testMarket(),
		persistence,
		persistence,
		runtime.Config{QueueSize: 1, SnapshotEvery: 100},
	)
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := runner.Fund(context.Background(), domain.FundRequest{
			RequestID: "fund-1",
			AccountID: "alice",
			Asset:     "USDT",
			Amount:    1_000,
		})
		firstDone <- err
	}()
	select {
	case <-persistence.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first command did not enter persistence")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := runner.Fund(context.Background(), domain.FundRequest{
			RequestID: "fund-2",
			AccountID: "bob",
			Asset:     "USDT",
			Amount:    1_000,
		})
		secondDone <- err
	}()
	waitFor(t, func() bool { return runner.Status().QueueDepth == 1 })

	_, err = runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "fund-3",
		AccountID: "carol",
		Asset:     "USDT",
		Amount:    1_000,
	})
	if !errors.Is(err, runtime.ErrQueueFull) {
		t.Fatalf("queue full error = %v", err)
	}
	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closeDone <- runner.Close(ctx)
	}()
	waitFor(t, func() bool { return runner.Status().State == runtime.StateClosing })
	close(persistence.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := persistence.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !found || snapshot.Sequence != 2 || snapshot.MarketID != "BTC-USDT" {
		t.Fatalf("graceful snapshot = found=%t %+v", found, snapshot)
	}
	if _, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "after-close",
		AccountID: "alice",
		Asset:     "USDT",
		Amount:    1,
	}); !errors.Is(err, runtime.ErrClosed) {
		t.Fatalf("after-close error = %v", err)
	}
}

func TestMarketRunnerUsesConfiguredSnapshotTimeout(t *testing.T) {
	t.Parallel()

	persistence := &deadlineSnapshotStore{
		Memory:    store.NewMemory(),
		deadlines: make(chan time.Duration, 2),
	}
	const snapshotTimeout = 45 * time.Second
	runner, err := runtime.NewMarketRunner(
		context.Background(),
		testMarket(),
		persistence,
		persistence,
		runtime.Config{
			QueueSize:       8,
			SnapshotEvery:   1,
			SnapshotTimeout: snapshotTimeout,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRunner(t, runner)

	if _, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "fund-snapshot-timeout",
		AccountID: "alice",
		Asset:     "USDT",
		Amount:    1_000,
	}); err != nil {
		t.Fatal(err)
	}

	remaining := <-persistence.deadlines
	if remaining < 40*time.Second || remaining > snapshotTimeout {
		t.Fatalf("snapshot deadline remaining = %s, want close to %s", remaining, snapshotTimeout)
	}
	if status := runner.Status(); status.LastError != "" {
		t.Fatalf("runner status after snapshot = %+v", status)
	}
}

func closeRunner(t *testing.T, runner *runtime.MarketRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func testMarket() domain.Market {
	return domain.Market{
		ID:                 "BTC-USDT",
		BaseAsset:          "BTC",
		QuoteAsset:         "USDT",
		BaseScale:          1,
		QuoteScale:         1,
		PriceTick:          1,
		QuantityStep:       1,
		MinQuantity:        1,
		MinNotional:        1,
		MakerFeeBPS:        10,
		TakerFeeBPS:        20,
		ConfigurationEpoch: 1,
	}
}
