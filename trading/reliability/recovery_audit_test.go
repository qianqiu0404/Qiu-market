package reliability_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/reliability"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestLedgerAndRecoveryProof(t *testing.T) {
	live, persistence := newDirectExchange(t)
	runRecoveryScenario(t, live, func(sequence uint64) {
		if sequence == 3 {
			if _, err := live.SaveSnapshot(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	})

	liveHash, err := live.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := reliability.ProveRecovery(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof.RestoredSequence != live.Sequence() ||
		proof.RestoredStateHash != liveHash ||
		proof.Ledger.FinalStateHash != liveHash {
		t.Fatalf("recovery proof = %+v live sequence/hash=%d/%s",
			proof, live.Sequence(), liveHash)
	}
	if proof.Ledger.Records != int(live.Sequence()) ||
		proof.Ledger.Transactions == 0 ||
		proof.Ledger.Entries < proof.Ledger.Transactions*2 {
		t.Fatalf("ledger proof counts = %+v", proof.Ledger)
	}
	if proof.Ledger.AssetNet["BTC"] != 0 ||
		proof.Ledger.AssetNet["USDT"] != 0 {
		t.Fatalf("asset conservation = %+v", proof.Ledger.AssetNet)
	}
}

func TestCommitThenCrashBeforeApplyRecoversExactlyOnce(t *testing.T) {
	persistence := store.NewMemory()
	crashStore := &commitThenCrashStore{delegate: persistence}
	live, err := exchange.New(reliabilityMarket(), crashStore, persistence)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.FundRequest{
		RequestID: "fund-commit-before-crash",
		AccountID: "alice",
		Asset:     "USDT",
		Amount:    25_000,
	}

	runUntilSimulatedCrash(t, func() {
		_, _ = live.Fund(context.Background(), request)
	})
	if live.Sequence() != 0 ||
		live.Balance("alice", "USDT") != (exchange.BalanceView{}) ||
		persistence.RecordCount() != 1 {
		t.Fatalf("pre-restart memory/durable state = sequence=%d balance=%+v records=%d, want 0/zero/1",
			live.Sequence(), live.Balance("alice", "USDT"), persistence.RecordCount())
	}

	restored, err := exchange.Restore(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Sequence() != 1 ||
		restored.Balance("alice", "USDT") != (exchange.BalanceView{Available: 25_000}) {
		t.Fatalf("restored committed fund sequence/balance = %d/%+v",
			restored.Sequence(), restored.Balance("alice", "USDT"))
	}
	replayed, err := restored.Fund(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Sequence != 1 || persistence.RecordCount() != 1 {
		t.Fatalf("same-id post-crash replay = %+v records=%d",
			replayed, persistence.RecordCount())
	}

	records := recordsFrom(t, persistence)
	proof, err := reliability.AuditRecords(reliabilityMarket(), records)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Transactions != 1 || proof.AssetNet["USDT"] != 0 {
		t.Fatalf("post-crash ledger proof = %+v", proof)
	}
}

func TestCorruptSnapshotAndEventAreRejected(t *testing.T) {
	t.Run("snapshot hash", func(t *testing.T) {
		live, persistence := newDirectExchange(t)
		mustFundExchange(t, live, "snapshot-fund", "alice", "USDT", 10_000)
		if _, err := live.SaveSnapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
		corrupt := corruptSnapshotStore{delegate: persistence}
		if _, err := exchange.Restore(
			context.Background(),
			reliabilityMarket(),
			persistence,
			corrupt,
		); !errors.Is(err, exchange.ErrRecoveryDiverged) {
			t.Fatalf("corrupt snapshot error = %v", err)
		}
	})

	t.Run("event journal", func(t *testing.T) {
		live, persistence := newDirectExchange(t)
		mustFundExchange(t, live, "event-fund", "alice", "USDT", 10_000)
		if err := persistence.CorruptRecord(1, func(record *store.Record) {
			record.Journal[0].Entries[0].Amount++
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := exchange.Restore(
			context.Background(),
			reliabilityMarket(),
			persistence,
			persistence,
		); !errors.Is(err, exchange.ErrRecoveryDiverged) {
			t.Fatalf("corrupt event error = %v", err)
		}
		if _, err := reliability.AuditRecords(
			reliabilityMarket(),
			recordsFrom(t, persistence),
		); !errors.Is(err, reliability.ErrLedgerProofFailed) {
			t.Fatalf("corrupt event audit error = %v", err)
		}
	})
}

func TestFinalStateHashDeterministicAcrossRecoveryPaths(t *testing.T) {
	first, firstStore := newDirectExchange(t)
	runRecoveryScenario(t, first, func(sequence uint64) {
		if sequence == 3 {
			if _, err := first.SaveSnapshot(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	})
	firstHash, err := first.StateHash()
	if err != nil {
		t.Fatal(err)
	}

	snapshotTail, err := exchange.Restore(
		context.Background(),
		reliabilityMarket(),
		firstStore,
		firstStore,
	)
	if err != nil {
		t.Fatal(err)
	}
	eventOnly, err := exchange.Restore(
		context.Background(),
		reliabilityMarket(),
		firstStore,
		emptySnapshotStore{},
	)
	if err != nil {
		t.Fatal(err)
	}

	second, _ := newDirectExchange(t)
	runRecoveryScenario(t, second, nil)
	secondHash, err := second.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	snapshotTailHash, err := snapshotTail.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	eventOnlyHash, err := eventOnly.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash ||
		firstHash != snapshotTailHash ||
		firstHash != eventOnlyHash {
		t.Fatalf("deterministic hashes live1=%s live2=%s snapshot-tail=%s event-only=%s",
			firstHash, secondHash, snapshotTailHash, eventOnlyHash)
	}
}

type commitThenCrashStore struct {
	delegate *store.Memory
	crashed  bool
}

func (s *commitThenCrashStore) Append(
	ctx context.Context,
	expectedSequence uint64,
	record store.Record,
) error {
	if err := s.delegate.Append(ctx, expectedSequence, record); err != nil {
		return err
	}
	if !s.crashed {
		s.crashed = true
		panic("simulated process crash after durable append")
	}
	return nil
}

func (s *commitThenCrashStore) RecordsAfter(
	ctx context.Context,
	sequence uint64,
) ([]store.Record, error) {
	return s.delegate.RecordsAfter(ctx, sequence)
}

type corruptSnapshotStore struct {
	delegate *store.Memory
}

func (s corruptSnapshotStore) Save(ctx context.Context, snapshot store.Snapshot) error {
	return s.delegate.Save(ctx, snapshot)
}

func (s corruptSnapshotStore) Load(ctx context.Context) (store.Snapshot, bool, error) {
	snapshot, found, err := s.delegate.Load(ctx)
	if err != nil || !found {
		return snapshot, found, err
	}
	snapshot.StateHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	return snapshot, true, nil
}

type emptySnapshotStore struct{}

func (emptySnapshotStore) Save(context.Context, store.Snapshot) error {
	return nil
}

func (emptySnapshotStore) Load(context.Context) (store.Snapshot, bool, error) {
	return store.Snapshot{}, false, nil
}

func newDirectExchange(t *testing.T) (*exchange.Exchange, *store.Memory) {
	t.Helper()
	persistence := store.NewMemory()
	live, err := exchange.New(reliabilityMarket(), persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	return live, persistence
}

func runUntilSimulatedCrash(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected simulated process crash")
		}
		if fmt.Sprint(recovered) != "simulated process crash after durable append" {
			t.Fatalf("unexpected panic = %v", recovered)
		}
	}()
	run()
}

func runRecoveryScenario(
	t *testing.T,
	live *exchange.Exchange,
	afterCommand func(sequence uint64),
) {
	t.Helper()
	mustFundExchange(t, live, "fund-seller-proof", "seller", "BTC", makerQuantity)
	mustFundExchange(t, live, "fund-buyer-proof", "buyer", "USDT", buyerFunding)
	maker, err := live.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "maker-proof",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         tradePrice,
		Quantity:      makerQuantity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterCommand != nil {
		afterCommand(live.Sequence())
	}
	if _, err := live.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "taker-proof",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         tradePrice,
		Quantity:      partialQuantity,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-proof",
		AccountID: "seller",
		OrderID:   maker.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := live.Validate(); err != nil {
		t.Fatal(err)
	}
}

func mustFundExchange(
	t *testing.T,
	live *exchange.Exchange,
	requestID string,
	accountID domain.AccountID,
	asset domain.Asset,
	amount int64,
) {
	t.Helper()
	if _, err := live.Fund(context.Background(), domain.FundRequest{
		RequestID: requestID,
		AccountID: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}
