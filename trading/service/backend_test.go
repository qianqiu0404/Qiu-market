package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	"github.com/the-web3/s78-market-services/trading/outbox"
	"github.com/the-web3/s78-market-services/trading/recovery"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func TestValidateConfigRequiresLoopback(t *testing.T) {
	t.Parallel()
	valid := Config{
		PostgresURL:       "postgres://example.invalid/s78",
		GRPCAddress:       "127.0.0.1:9094",
		CursorHMACCurrent: "test:" + strings.Repeat("A", 43),
	}
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	valid.GRPCAddress = "0.0.0.0:9094"
	if err := validateConfig(valid); err == nil {
		t.Fatal("accepted non-loopback trading listener")
	}
}

func TestValidateConfigRequiresPersistentCursorKey(t *testing.T) {
	t.Parallel()
	config := Config{
		PostgresURL: "postgres://example.invalid/s78",
		GRPCAddress: "127.0.0.1:9094",
	}
	if err := validateConfig(config); err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("missing cursor key did not fail closed: %v", err)
	}
}

func TestRandomRequestPrefixIsUnique(t *testing.T) {
	t.Parallel()
	first, err := randomRequestPrefix()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomRequestPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("demo-maker request prefix repeated")
	}
}

func TestDemoMakerDepletedBalanceUsesNewStartupScope(t *testing.T) {
	ctx := context.Background()
	market := domain.DefaultBTCUSDTMarket()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		ctx, market, memory, memory, tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	if err := bootstrapDemoMaker(ctx, runner, market, false, "legacy-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Fund(ctx, domain.FundRequest{
		RequestID: "fund-depletion-buyer", AccountID: "depletion-buyer",
		Asset: market.QuoteAsset, Amount: demoMakerUSDT,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "depletion-bid", AccountID: "depletion-buyer",
		Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		TimeInForce: domain.TimeInForceGTC,
		Price:       60_000_000_000, Quantity: demoMakerBTC,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "deplete-maker-btc", AccountID: demoMakerAccount,
		Side: domain.SideSell, Type: domain.OrderTypeMarket,
		TimeInForce: domain.TimeInForceIOC, Quantity: demoMakerBTC,
	}); err != nil {
		t.Fatal(err)
	}
	depleted, err := runner.Balance(demoMakerAccount, market.BaseAsset)
	if err != nil {
		t.Fatal(err)
	}
	if depleted.Available != 0 || depleted.Held != 0 {
		t.Fatalf("demo maker BTC was not depleted = %+v", depleted)
	}
	if err := bootstrapDemoMaker(ctx, runner, market, false, "legacy-v1"); err == nil || !strings.Contains(err.Error(), "remains depleted") {
		t.Fatalf("same-scope idempotent depletion error = %v", err)
	}
	if err := bootstrapDemoMaker(ctx, runner, market, false, "startup-2"); err != nil {
		t.Fatal(err)
	}
	replenished, err := runner.Balance(demoMakerAccount, market.BaseAsset)
	if err != nil {
		t.Fatal(err)
	}
	if replenished.Available != demoMakerBTC || replenished.Held != 0 {
		t.Fatalf("new startup scope did not replenish BTC = %+v", replenished)
	}
}

func TestRecoveryControlledMakerStartsOnlyWhenWritableAndUnwindsOffline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	market := domain.DefaultBTCUSDTMarket()
	provenance := recovery.Provenance{
		ProductionOrigin: "https://qiu-market.example", DeploymentID: "dpl_servicetest",
		DeploymentURL: "https://qiu-market-service-test.vercel.app",
		ReleaseCommit: strings.Repeat("d", 40), SourceDigest: strings.Repeat("e", 64),
	}
	coordinator, err := recovery.NewCoordinator(recovery.NewMemoryStore(), market.ID, provenance)
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
	memory := store.NewMemory()
	runnerConfig := tradingruntime.DefaultConfig()
	runnerConfig.WriteGate = coordinator
	runner, err := tradingruntime.NewMarketRunner(ctx, market, memory, memory, runnerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	if err := bootstrapDemoMaker(ctx, runner, market, true, "test-epoch"); err != nil {
		t.Fatal(err)
	}
	fingerprint := runner.Status()
	proof := recovery.Proof{
		RuntimeSequence:    fingerprint.Sequence,
		StateHash:          fingerprint.StateHash,
		LedgerBalanced:     true,
		EventContinuous:    true,
		ProjectionCaughtUp: true,
		OutboxCaughtUp:     true,
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
	publisher, err := outbox.New(&readyOutboxStore{}, outbox.Config{
		BatchSize: 1, PollEvery: time.Millisecond, MaximumRetryDelay: time.Millisecond,
		CleanupEvery: time.Hour, PublishedRetention: time.Hour,
		CleanupBatchSize: 1, MaximumCleanupRuns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	publisherContext, publisherCancel := context.WithCancel(ctx)
	defer publisherCancel()
	go publisher.Run(publisherContext)
	waitUntil(t, func() bool { return publisher.Status().State == "ready" })
	makerConfig := marketmaker.DefaultConfig()
	makerConfig.RequestPrefix = "recovery-maker-test"
	makerConfig.RefreshEvery = time.Hour
	backend := &Backend{
		runner:       runner,
		publisher:    publisher,
		recovery:     coordinator,
		makerEnabled: true,
		makerSource:  fixedMakerReference{price: 60_000_000_000},
		makerConfig:  makerConfig,
	}
	if err := backend.reconcileRecoveryLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.makerRunning() {
		t.Fatal("demo maker started before writable promotion")
	}
	if orders, _ := runner.Orders(demoMakerAccount, true); len(orders) != 0 {
		t.Fatalf("warmup demo-maker orders = %d", len(orders))
	}
	status, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
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
		EvidenceSHA256: strings.Repeat("f", 64),
		Provenance:     status.Provenance,
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.reconcileRecoveryLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		orders, orderErr := runner.Orders(demoMakerAccount, true)
		return orderErr == nil && len(orders) == 6
	})
	if _, err := coordinator.Fail(ctx, recovery.PhaseOffline, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if err := backend.reconcileRecoveryLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.makerRunning() {
		t.Fatal("demo maker remained running after offline transition")
	}
	orders, err := runner.Orders(demoMakerAccount, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("offline demo-maker orders = %d", len(orders))
	}
}

func TestBackendRejectsConfiguredSourceDigestMismatchBeforeStartup(t *testing.T) {
	_, err := New(context.Background(), Config{
		RecoveryGate: true,
		RecoveryProvenance: recovery.Provenance{
			SourceDigest: strings.Repeat("f", 64),
		},
	}, func(error) {})
	if err == nil || !strings.Contains(err.Error(), "does not match current executable") {
		t.Fatalf("source digest mismatch error = %v", err)
	}
}

type fixedMakerReference struct{ price int64 }

func (s fixedMakerReference) Current(context.Context) (marketmaker.Reference, error) {
	return marketmaker.Reference{Price: s.price, ObservedAt: time.Now().UTC()}, nil
}

type readyOutboxStore struct{}

func (*readyOutboxStore) OutboxCheckpoint(context.Context) (postgresstore.Cursor, bool, error) {
	return postgresstore.Cursor{}, false, nil
}

func (*readyOutboxStore) PublishOutboxBatch(context.Context, int) (postgresstore.PublishResult, error) {
	return postgresstore.PublishResult{}, nil
}

func (*readyOutboxStore) CleanupPublishedOutbox(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
