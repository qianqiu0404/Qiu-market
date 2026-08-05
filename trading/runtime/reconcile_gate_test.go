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

type blockingRecoveryStore struct {
	*store.Memory

	mu              sync.Mutex
	failNextAppend  bool
	blockNextReplay bool
	replayEntered   chan struct{}
	replayRelease   chan struct{}
	releaseOnce     sync.Once
}

func (s *blockingRecoveryStore) Append(
	ctx context.Context,
	expectedSequence uint64,
	record store.Record,
) error {
	if err := s.Memory.Append(ctx, expectedSequence, record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failNextAppend {
		return nil
	}
	s.failNextAppend = false
	s.blockNextReplay = true
	return errCommitOutcomeUnknown
}

func (s *blockingRecoveryStore) RecordsAfter(
	ctx context.Context,
	sequence uint64,
) ([]store.Record, error) {
	s.mu.Lock()
	block := s.blockNextReplay
	if block {
		s.blockNextReplay = false
		close(s.replayEntered)
	}
	s.mu.Unlock()
	if block {
		select {
		case <-s.replayRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Memory.RecordsAfter(ctx, sequence)
}

func (s *blockingRecoveryStore) releaseReplay() {
	s.releaseOnce.Do(func() {
		close(s.replayRelease)
	})
}

func TestMarketRunnerRejectsWritesUntilReconcileCompletes(t *testing.T) {
	persistence := &blockingRecoveryStore{
		Memory:         store.NewMemory(),
		failNextAppend: true,
		replayEntered:  make(chan struct{}),
		replayRelease:  make(chan struct{}),
	}
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
	defer func() {
		persistence.releaseReplay()
		closeRunner(t, runner)
	}()

	firstRequest := domain.FundRequest{
		RequestID: "commit-before-reconcile",
		AccountID: "alice",
		Asset:     "USDT",
		Amount:    1_000,
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := runner.Fund(context.Background(), firstRequest)
		firstDone <- err
	}()

	select {
	case <-persistence.replayEntered:
	case <-time.After(time.Second):
		t.Fatal("runner did not enter event reconciliation")
	}
	if status := runner.Status(); status.State != runtime.StateRecovering {
		t.Fatalf("runner status while replay is blocked = %+v", status)
	}

	writeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = runner.Fund(writeContext, domain.FundRequest{
		RequestID: "must-not-queue-during-reconcile",
		AccountID: "bob",
		Asset:     "USDT",
		Amount:    1_000,
	})
	if !errors.Is(err, runtime.ErrUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write during reconcile error = %v", err)
	}
	if persistence.RecordCount() != 1 {
		t.Fatalf("write during reconcile changed durable record count = %d", persistence.RecordCount())
	}

	persistence.releaseReplay()
	if err := <-firstDone; !errors.Is(err, errCommitOutcomeUnknown) ||
		!errors.Is(err, exchange.ErrPersistence) {
		t.Fatalf("unknown commit result = %v", err)
	}
	if status := runner.Status(); status.State != runtime.StateReady ||
		status.Sequence != 1 ||
		status.RecoveryCount != 1 {
		t.Fatalf("runner status after reconcile = %+v", status)
	}

	replayed, err := runner.Fund(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Sequence != 1 || persistence.RecordCount() != 1 {
		t.Fatalf("same-id replay after reconcile = %+v records=%d",
			replayed, persistence.RecordCount())
	}
	second, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "write-after-reconcile",
		AccountID: "bob",
		Asset:     "USDT",
		Amount:    1_000,
	})
	if err != nil || second.Sequence != 2 {
		t.Fatalf("write after reconcile = %+v error=%v", second, err)
	}
}
