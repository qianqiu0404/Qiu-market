package recovery

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

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
	if _, err := coordinator.Advance(ctx, PhaseWritable, proof); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("generic writable transition error = %v", err)
	}
	if _, err := coordinator.Promote(
		ctx,
		bindingFromStatus(status),
		TransportEvidence{},
	); !errors.Is(err, ErrTransportEvidence) {
		t.Fatalf("incomplete transport evidence error = %v", err)
	}
	status, err = coordinator.Promote(
		ctx,
		bindingFromStatus(status),
		completeTransportEvidence(),
	)
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

func TestCoordinatorPromotionRejectsEveryStaleBindingField(t *testing.T) {
	ctx := context.Background()
	coordinator, _ := NewCoordinator(NewMemoryStore(), domain.MarketID("BTC-USDT"))
	status, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []Phase{
		PhaseDependenciesReady, PhaseTradingReplay, PhaseReconciling,
		PhaseReadOnly, PhaseTransportWarmup,
	} {
		proof := Proof{}
		if phase == PhaseReadOnly || phase == PhaseTransportWarmup {
			proof = completeProof(false)
		}
		status, err = coordinator.Advance(ctx, phase, proof)
		if err != nil {
			t.Fatal(err)
		}
	}
	valid := bindingFromStatus(status)
	mutations := []func(*Binding){
		func(value *Binding) { value.MarketID = "ETH-USDT" },
		func(value *Binding) { value.EpochID += "-stale" },
		func(value *Binding) { value.Version++ },
		func(value *Binding) { value.RuntimeSequence++ },
		func(value *Binding) { value.StateHash = strings.Repeat("b", 64) },
	}
	for _, mutate := range mutations {
		stale := valid
		mutate(&stale)
		if _, err := coordinator.Promote(
			ctx, stale, completeTransportEvidence(),
		); !errors.Is(err, ErrBindingMismatch) {
			t.Fatalf("stale binding %+v error = %v", stale, err)
		}
	}
	current, err := coordinator.Status(ctx)
	if err != nil || current.WritesEnabled || current.Version != valid.Version {
		t.Fatalf("stale promotion changed state: %+v err=%v", current, err)
	}
}

func TestCoordinatorRejectsMalformedTransportEvidence(t *testing.T) {
	ctx := context.Background()
	coordinator, _ := NewCoordinator(NewMemoryStore(), domain.MarketID("BTC-USDT"))
	warmup := advanceToWarmup(t, ctx, coordinator)
	tests := []struct {
		name   string
		mutate func(*TransportEvidence)
	}{
		{
			name: "digest is not hexadecimal",
			mutate: func(value *TransportEvidence) {
				value.EvidenceSHA256 = strings.Repeat("z", 64)
			},
		},
		{
			name: "gap duration overflows",
			mutate: func(value *TransportEvidence) {
				value.MaximumGapMS = math.MaxInt64
			},
		},
		{
			name: "sample span cannot fit reported gaps",
			mutate: func(value *TransportEvidence) {
				value.FirstSampleAt = value.LastSampleAt.Add(-time.Minute)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeTransportEvidence()
			test.mutate(&evidence)
			if _, err := coordinator.Promote(
				ctx, bindingFromStatus(warmup), evidence,
			); !errors.Is(err, ErrTransportEvidence) {
				t.Fatalf("malformed transport evidence error = %v", err)
			}
		})
	}
	current, err := coordinator.Status(ctx)
	if err != nil || current.Phase != PhaseTransportWarmup || current.WritesEnabled {
		t.Fatalf("malformed evidence changed state = %+v err=%v", current, err)
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

func TestCoordinatorStoreContinuityLatchRequiresNewEpoch(t *testing.T) {
	ctx := context.Background()
	store := &continuityFaultStore{base: NewMemoryStore()}
	coordinator, _ := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	warmup := advanceToWarmup(t, ctx, coordinator)
	if _, err := coordinator.Promote(
		ctx, bindingFromStatus(warmup), completeTransportEvidence(),
	); err != nil {
		t.Fatal(err)
	}
	store.setFailLoad(true)
	if err := coordinator.RequireWritable(ctx); !errors.Is(err, ErrWriteBlocked) {
		t.Fatalf("store outage write gate = %v", err)
	}
	store.setFailLoad(false)
	status, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != PhaseWritable || status.WritesEnabled ||
		status.Proof.TransportHealthy || !status.ContinuityUncertain {
		t.Fatalf("latched effective status = %+v", status)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsFound <- coordinator.RequireWritable(ctx)
		}()
	}
	wait.Wait()
	close(errorsFound)
	for gateErr := range errorsFound {
		if !errors.Is(gateErr, ErrWriteBlocked) {
			t.Fatalf("concurrent latched gate = %v", gateErr)
		}
	}
	next, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.EpochID == warmup.EpochID || next.ContinuityUncertain || next.WritesEnabled {
		t.Fatalf("new epoch did not clear latch: %+v", next)
	}
}

func TestCoordinatorWarmupOutageCannotReuseOldTransportProof(t *testing.T) {
	ctx := context.Background()
	store := &continuityFaultStore{base: NewMemoryStore()}
	coordinator, _ := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	warmup := advanceToWarmup(t, ctx, coordinator)
	store.setFailLoad(true)
	if _, err := coordinator.Status(ctx); err == nil {
		t.Fatal("warmup store outage was not observed")
	}
	store.setFailLoad(false)
	if _, err := coordinator.Promote(
		ctx, bindingFromStatus(warmup), completeTransportEvidence(),
	); !errors.Is(err, ErrWriteBlocked) {
		t.Fatalf("warmup outage promotion error = %v", err)
	}
	status, err := coordinator.Status(ctx)
	if err != nil || status.WritesEnabled || !status.ContinuityUncertain {
		t.Fatalf("warmup latch status = %+v err=%v", status, err)
	}
}

func TestCoordinatorUncertainPromotionCommitLatchesSameEpoch(t *testing.T) {
	ctx := context.Background()
	store := &continuityFaultStore{base: NewMemoryStore()}
	coordinator, _ := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	warmup := advanceToWarmup(t, ctx, coordinator)
	store.setFailSave(true)
	if _, err := coordinator.Promote(
		ctx, bindingFromStatus(warmup), completeTransportEvidence(),
	); err == nil {
		t.Fatal("injected uncertain promotion succeeded")
	}
	store.setFailSave(false)
	status, err := coordinator.Status(ctx)
	if err != nil || !status.ContinuityUncertain || status.WritesEnabled {
		t.Fatalf("uncertain promotion status = %+v err=%v", status, err)
	}
	if _, err := coordinator.Promote(
		ctx, bindingFromStatus(warmup), completeTransportEvidence(),
	); !errors.Is(err, ErrWriteBlocked) {
		t.Fatalf("uncertain promotion retry error = %v", err)
	}
}

type continuityFaultStore struct {
	mu       sync.RWMutex
	base     *MemoryStore
	failLoad bool
	failSave bool
}

func (s *continuityFaultStore) Load(
	ctx context.Context,
	marketID domain.MarketID,
) (Status, bool, error) {
	s.mu.RLock()
	fail := s.failLoad
	s.mu.RUnlock()
	if fail {
		return Status{}, false, errors.New("injected recovery store outage")
	}
	return s.base.Load(ctx, marketID)
}

func (s *continuityFaultStore) Save(
	ctx context.Context,
	expected uint64,
	next Status,
) error {
	s.mu.RLock()
	fail := s.failSave
	s.mu.RUnlock()
	if fail {
		return errors.New("injected uncertain recovery save")
	}
	return s.base.Save(ctx, expected, next)
}

func (s *continuityFaultStore) setFailLoad(value bool) {
	s.mu.Lock()
	s.failLoad = value
	s.mu.Unlock()
}

func (s *continuityFaultStore) setFailSave(value bool) {
	s.mu.Lock()
	s.failSave = value
	s.mu.Unlock()
}

func advanceToWarmup(
	t *testing.T,
	ctx context.Context,
	coordinator *Coordinator,
) Status {
	t.Helper()
	status, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []Phase{
		PhaseDependenciesReady, PhaseTradingReplay, PhaseReconciling,
		PhaseReadOnly, PhaseTransportWarmup,
	} {
		proof := Proof{}
		if phase == PhaseReadOnly || phase == PhaseTransportWarmup {
			proof = completeProof(false)
		}
		status, err = coordinator.Advance(ctx, phase, proof)
		if err != nil {
			t.Fatal(err)
		}
	}
	return status
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

func bindingFromStatus(status Status) Binding {
	return Binding{
		MarketID: status.MarketID, EpochID: status.EpochID, Version: status.Version,
		RuntimeSequence: status.Proof.RuntimeSequence, StateHash: status.Proof.StateHash,
	}
}

func completeTransportEvidence() TransportEvidence {
	last := time.Now().UTC()
	first := last.Add(-MinimumTransportWindow)
	return TransportEvidence{
		SampleCount:    MinimumTransportSamples,
		FirstSampleAt:  first,
		LastSampleAt:   last,
		MaximumGapMS:   MaximumTransportGap.Milliseconds(),
		EvidenceSHA256: strings.Repeat("c", 64),
	}
}
