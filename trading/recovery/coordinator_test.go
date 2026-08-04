package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
)

func TestCoordinatorFailsClosedUntilCompleteProof(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	coordinator, err := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.WritesEnabled || !errors.Is(coordinator.RequireWritable(ctx), ErrWriteBlocked) {
		t.Fatalf("bootstrap write gate = %+v", status)
	}
	for _, phase := range []Phase{
		PhaseDependenciesReady,
		PhaseTradingReplay,
		PhaseReconciling,
	} {
		status, err = coordinator.Advance(ctx, phase, Proof{})
		if err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
	}
	if _, err := coordinator.Advance(ctx, PhaseReadOnly, Proof{}); !errors.Is(err, ErrProofIncomplete) {
		t.Fatalf("incomplete proof error = %v", err)
	}
	proof := completeProof(false)
	status, err = coordinator.Advance(ctx, PhaseReadOnly, proof)
	if err != nil {
		t.Fatal(err)
	}
	if status.WritesEnabled {
		t.Fatal("read-only phase enabled writes")
	}
	status, err = coordinator.Advance(ctx, PhaseTransportWarmup, proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Advance(ctx, PhaseWritable, proof); !errors.Is(err, ErrProofIncomplete) {
		t.Fatalf("unhealthy transport error = %v", err)
	}
	proof.TransportHealthy = true
	status, err = coordinator.Advance(ctx, PhaseWritable, proof)
	if err != nil {
		t.Fatal(err)
	}
	if !status.WritesEnabled || coordinator.RequireWritable(ctx) != nil {
		t.Fatalf("writable status = %+v", status)
	}
	if len(store.History(domain.MarketID("BTC-USDT"))) != 7 {
		t.Fatalf("history entries = %d", len(store.History(domain.MarketID("BTC-USDT"))))
	}
}

func TestCoordinatorFailureRemainsClosedAndNewEpochResetsProof(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	coordinator, _ := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	first, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := coordinator.Fail(ctx, PhaseManualReview, errors.New("state hash mismatch"))
	if err != nil {
		t.Fatal(err)
	}
	if failed.WritesEnabled || !strings.Contains(failed.LastError, "state hash mismatch") {
		t.Fatalf("failed status = %+v", failed)
	}
	if _, err := coordinator.Advance(ctx, PhaseWritable, completeProof(true)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v", err)
	}
	second, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.EpochID == first.EpochID || second.WritesEnabled || second.Proof != (Proof{}) {
		t.Fatalf("new epoch did not reset state: first=%+v second=%+v", first, second)
	}
}

func TestMemoryStoreRejectsStaleVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	status := Status{
		SchemaVersion: SchemaVersion,
		MarketID:      domain.MarketID("BTC-USDT"),
		EpochID:       "epoch",
		Phase:         PhaseBootstrap,
		Version:       1,
	}
	if err := store.Save(ctx, 0, status); err != nil {
		t.Fatal(err)
	}
	status.Version = 2
	if err := store.Save(ctx, 0, status); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale save error = %v", err)
	}
}

func completeProof(transport bool) Proof {
	return Proof{
		RuntimeSequence:    42,
		StateHash:          strings.Repeat("a", 64),
		LedgerBalanced:     true,
		EventContinuous:    true,
		ProjectionCaughtUp: true,
		OutboxCaughtUp:     true,
		TransportHealthy:   transport,
	}
}
