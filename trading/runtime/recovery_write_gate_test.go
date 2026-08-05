package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/recovery"
	"github.com/the-web3/s78-market-services/trading/store"
)

type recoveryGateBlockingStore struct {
	*store.Memory
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *recoveryGateBlockingStore) Append(
	ctx context.Context,
	expectedSequence uint64,
	record store.Record,
) error {
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return s.Memory.Append(ctx, expectedSequence, record)
}

func TestMarketRunnerRecoveryGateBlocksEveryMutationButLeavesReadsAvailable(t *testing.T) {
	ctx := context.Background()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
		runtimeTestProvenance(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.WriteGate = coordinator
	memory := store.NewMemory()
	runner, err := NewMarketRunner(
		ctx,
		domain.DefaultBTCUSDTMarket(),
		memory,
		memory,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()

	if status := runner.Status(); status.State != StateReady {
		t.Fatalf("read status = %+v", status)
	}
	if _, err := runner.Depth(20); err != nil {
		t.Fatalf("read orderbook while gated: %v", err)
	}
	if _, err := runner.Fund(ctx, domain.FundRequest{
		RequestID: "fund",
		AccountID: "user",
		Asset:     "USDT",
		Amount:    1_000_000,
	}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("fund gate error = %v", err)
	}
	if _, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "submit",
		AccountID:     "user",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000_000_000,
		Quantity:      100_000,
	}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("submit gate error = %v", err)
	}
	if _, err := runner.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel",
		AccountID: "user",
		OrderID:   "O-1",
	}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("cancel gate error = %v", err)
	}
	if runner.Status().Sequence != 0 {
		t.Fatalf("gated mutation advanced sequence: %+v", runner.Status())
	}
	proof := recovery.Proof{
		RuntimeSequence:    0,
		StateHash:          strings.Repeat("a", 64),
		LedgerBalanced:     true,
		EventContinuous:    true,
		ProjectionCaughtUp: true,
		OutboxCaughtUp:     true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady,
		recovery.PhaseTradingReplay,
		recovery.PhaseReconciling,
		recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		if _, err := coordinator.Advance(ctx, phase, proof); err != nil {
			t.Fatalf("advance recovery to %s: %v", phase, err)
		}
	}
	status, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	warmupRuntime := runner.Status()
	if _, err := runner.CancelSafety(ctx, domain.CancelOrder{
		RequestID: "warmup-safety-cancel",
		AccountID: "system:demo-maker",
		OrderID:   "O-1",
	}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("transport warmup safety cancel gate error = %v", err)
	}
	if _, err := runner.FundSafety(ctx, domain.FundRequest{
		RequestID: "warmup-safety-fund",
		AccountID: "system:demo-maker",
		Asset:     "USDT",
		Amount:    1_000_000,
	}); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("transport warmup safety fund gate error = %v", err)
	}
	if after := runner.Status(); after.Sequence != warmupRuntime.Sequence ||
		after.StateHash != warmupRuntime.StateHash {
		t.Fatalf("transport warmup mutation changed runtime: before=%+v after=%+v", warmupRuntime, after)
	}
	first := time.Now().UTC().Add(-recovery.MinimumTransportWindow)
	if _, err := coordinator.Promote(ctx, recovery.Binding{
		MarketID: status.MarketID, EpochID: status.EpochID, Version: status.Version,
		RuntimeSequence: status.Proof.RuntimeSequence, StateHash: status.Proof.StateHash,
		Provenance: status.Provenance,
	}, recovery.TransportEvidence{
		SampleCount:   recovery.MinimumTransportSamples,
		FirstSampleAt: first, LastSampleAt: time.Now().UTC(),
		MaximumGapMS:   recovery.MaximumTransportGap.Milliseconds(),
		EvidenceSHA256: strings.Repeat("b", 64),
		Provenance:     status.Provenance,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Fund(ctx, domain.FundRequest{
		RequestID: "fund",
		AccountID: "user",
		Asset:     "USDT",
		Amount:    1_000_000,
	}); err != nil {
		t.Fatalf("fund after recovery proof: %v", err)
	}
	if runner.Status().Sequence != 1 {
		t.Fatalf("writable mutation sequence = %+v", runner.Status())
	}
}

func TestQueuedCommandCannotCrossRecoveryVersionChange(t *testing.T) {
	ctx := context.Background()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
		runtimeTestProvenance(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady,
		recovery.PhaseTradingReplay,
	} {
		if _, err := coordinator.Advance(ctx, phase, recovery.Proof{}); err != nil {
			t.Fatal(err)
		}
	}
	persistence := &recoveryGateBlockingStore{
		Memory: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{}),
	}
	config := DefaultConfig()
	config.WriteGate = coordinator
	runner, err := NewMarketRunner(
		ctx, domain.DefaultBTCUSDTMarket(), persistence, persistence, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	fingerprint := runner.Status()
	proof := recovery.Proof{
		RuntimeSequence: fingerprint.Sequence, StateHash: fingerprint.StateHash,
		LedgerBalanced: true, EventContinuous: true,
		ProjectionCaughtUp: true, OutboxCaughtUp: true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseReconciling,
		recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		if _, err := coordinator.Advance(ctx, phase, proof); err != nil {
			t.Fatal(err)
		}
	}
	warmup, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := time.Now().UTC()
	if _, err := coordinator.Promote(ctx, recovery.Binding{
		MarketID: warmup.MarketID, EpochID: warmup.EpochID, Version: warmup.Version,
		RuntimeSequence: warmup.Proof.RuntimeSequence, StateHash: warmup.Proof.StateHash,
		Provenance: warmup.Provenance,
	}, recovery.TransportEvidence{
		SampleCount:   recovery.MinimumTransportSamples,
		FirstSampleAt: last.Add(-recovery.MinimumTransportWindow), LastSampleAt: last,
		MaximumGapMS:   recovery.MaximumTransportGap.Milliseconds(),
		EvidenceSHA256: strings.Repeat("b", 64),
		Provenance:     warmup.Provenance,
	}); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, commandErr := runner.Fund(ctx, domain.FundRequest{
			RequestID: "active-before-offline", AccountID: "alice",
			Asset: "USDT", Amount: 1_000_000,
		})
		firstDone <- commandErr
	}()
	select {
	case <-persistence.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("active command did not reach persistence")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, commandErr := runner.Fund(ctx, domain.FundRequest{
			RequestID: "queued-before-offline", AccountID: "bob",
			Asset: "USDT", Amount: 1_000_000,
		})
		secondDone <- commandErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for runner.Status().QueueDepth != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runner.Status().QueueDepth != 1 {
		t.Fatal("second command did not queue")
	}
	if _, err := coordinator.Fail(ctx, recovery.PhaseOffline, errors.New("transport lost")); err != nil {
		t.Fatal(err)
	}
	close(persistence.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("already-active command resolution = %v", err)
	}
	if err := <-secondDone; !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("queued stale admission error = %v", err)
	}
	if got := runner.Status().Sequence; got != 1 {
		t.Fatalf("stale queued command advanced sequence to %d", got)
	}
	bob, err := runner.Balance("bob", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if bob.Available != 0 || bob.Held != 0 {
		t.Fatalf("stale queued command changed balance = %+v", bob)
	}
}

func runtimeTestProvenance() recovery.Provenance {
	return recovery.Provenance{
		ProductionOrigin: "https://qiu-market.example", DeploymentID: "dpl_runtimetest",
		DeploymentURL: "https://qiu-market-runtime-test.vercel.app",
		ReleaseCommit: strings.Repeat("d", 40), SourceDigest: strings.Repeat("e", 64),
	}
}
