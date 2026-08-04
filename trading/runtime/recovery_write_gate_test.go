package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/recovery"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestMarketRunnerRecoveryGateBlocksEveryMutationButLeavesReadsAvailable(t *testing.T) {
	ctx := context.Background()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
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
	proof.TransportHealthy = true
	if _, err := coordinator.Advance(ctx, recovery.PhaseWritable, proof); err != nil {
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
