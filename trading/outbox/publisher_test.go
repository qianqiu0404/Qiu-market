package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/outbox"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

type fakeStore struct {
	mu             sync.Mutex
	pending        int
	failPublish    int
	publishCalls   int
	cleanupCalls   int
	cleanupDeleted int64
	checkpoint     postgresstore.Cursor
	failCleanup    bool
}

func (s *fakeStore) OutboxCheckpoint(
	_ context.Context,
) (postgresstore.Cursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoint, s.checkpoint.Sequence > 0, nil
}

func (s *fakeStore) PublishOutboxBatch(
	_ context.Context,
	limit int,
) (postgresstore.PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishCalls++
	if s.failPublish > 0 {
		s.failPublish--
		return postgresstore.PublishResult{}, errors.New("temporary database error")
	}
	published := min(s.pending, limit)
	s.pending -= published
	result := postgresstore.PublishResult{
		Published: published,
		Checkpoint: postgresstore.Cursor{
			Sequence:   uint64(published),
			EventIndex: 1,
		},
	}
	if published > 0 {
		s.checkpoint = result.Checkpoint
	}
	return result, nil
}

func (s *fakeStore) CleanupPublishedOutbox(
	_ context.Context,
	_ time.Time,
	_ int,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCalls++
	if s.failCleanup {
		return 0, errors.New("cleanup unavailable")
	}
	deleted := s.cleanupDeleted
	s.cleanupDeleted = 0
	return deleted, nil
}

func (s *fakeStore) snapshot() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending, s.publishCalls, s.cleanupCalls
}

func TestPublisherDrainsBatches(t *testing.T) {
	store := &fakeStore{pending: 5}
	config := testConfig()
	config.BatchSize = 2
	publisher, err := outbox.New(store, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		publisher.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool {
		pending, calls, _ := store.snapshot()
		return pending == 0 && calls >= 3
	})
	status := publisher.Status()
	if status.State != "ready" || status.LastError != "" ||
		status.PublishedSinceBoot != 5 {
		t.Fatalf("publisher status = %+v", status)
	}
	cancel()
	<-done
}

func TestPublisherClearsCurrentErrorAfterRecovery(t *testing.T) {
	store := &fakeStore{pending: 1, failPublish: 1}
	publisher, err := outbox.New(store, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		publisher.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool {
		status := publisher.Status()
		return status.State == "ready" && status.PublishedSinceBoot == 1
	})
	status := publisher.Status()
	if status.LastError != "" ||
		status.LastIncident == "" ||
		status.LastIncidentAt.IsZero() {
		t.Fatalf("recovered publisher status = %+v", status)
	}
	cancel()
	<-done
}

func TestPublisherKeepsCleanupErrorUntilCleanupRecovers(t *testing.T) {
	store := &fakeStore{failCleanup: true}
	config := testConfig()
	config.CleanupEvery = 2 * time.Millisecond
	publisher, err := outbox.New(store, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		publisher.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool {
		return publisher.Status().State == "degraded"
	})
	_, callsBefore, _ := store.snapshot()
	time.Sleep(5 * time.Millisecond)
	_, callsAfter, _ := store.snapshot()
	status := publisher.Status()
	if callsAfter <= callsBefore || status.State != "degraded" ||
		status.LastError == "" {
		t.Fatalf("cleanup degradation was cleared by publish: status=%+v calls=%d/%d",
			status, callsBefore, callsAfter)
	}
	cancel()
	<-done
}

func testConfig() outbox.Config {
	return outbox.Config{
		BatchSize:          10,
		PollEvery:          time.Millisecond,
		MaximumRetryDelay:  4 * time.Millisecond,
		CleanupEvery:       time.Hour,
		PublishedRetention: time.Hour,
		CleanupBatchSize:   10,
		MaximumCleanupRuns: 1,
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}
