package ledger_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
)

func TestLedgerPostingAndSnapshot(t *testing.T) {
	t.Parallel()

	book := ledger.New()
	if err := book.FundVirtual("fund-1", "fund:alice", "alice", "USDT", 10_000); err != nil {
		t.Fatal(err)
	}
	if err := book.Hold("hold-1", "order:o1", "alice", "USDT", 6_000); err != nil {
		t.Fatal(err)
	}
	if err := book.Release("release-1", "cancel:o1", "alice", "USDT", 1_000); err != nil {
		t.Fatal(err)
	}
	available, held := book.UserBalance("alice", "USDT")
	if available != 5_000 || held != 5_000 {
		t.Fatalf("alice balance = available %d held %d", available, held)
	}
	if err := book.Validate(); err != nil {
		t.Fatalf("ledger validation: %v", err)
	}

	restored, err := ledger.FromSnapshot(book.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if gotAvailable, gotHeld := restored.UserBalance("alice", "USDT"); gotAvailable != available || gotHeld != held {
		t.Fatalf("restored balance = available %d held %d", gotAvailable, gotHeld)
	}
}

func TestLedgerRejectsUnbalancedAndNegativeTransactions(t *testing.T) {
	t.Parallel()

	book := ledger.New()
	err := book.Post(ledger.Transaction{
		ID:        "bad-unbalanced",
		Reference: "test",
		Entries: []ledger.Entry{
			{Account: "a", Asset: "USDT", Amount: -10},
			{Account: "b", Asset: "USDT", Amount: 9},
		},
	})
	if !errors.Is(err, ledger.ErrUnbalancedTransaction) && !errors.Is(err, ledger.ErrInsufficientBalance) {
		t.Fatalf("unbalanced transaction error = %v", err)
	}

	if err := book.FundVirtual("fund-1", "fund:alice", "alice", "BTC", 5); err != nil {
		t.Fatal(err)
	}
	err = book.Hold("hold-too-much", "order:o1", "alice", domain.Asset("BTC"), 6)
	if !errors.Is(err, ledger.ErrInsufficientBalance) {
		t.Fatalf("insufficient balance error = %v", err)
	}
	available, held := book.UserBalance("alice", "BTC")
	if available != 5 || held != 0 {
		t.Fatalf("failed transaction mutated balances: available=%d held=%d", available, held)
	}
}

func TestDuplicateTransactionIsRejected(t *testing.T) {
	t.Parallel()

	book := ledger.New()
	if err := book.FundVirtual("fund-1", "fund:alice", "alice", "USDT", 10); err != nil {
		t.Fatal(err)
	}
	if err := book.FundVirtual("fund-1", "fund:alice", "alice", "USDT", 10); !errors.Is(err, ledger.ErrDuplicateTransaction) {
		t.Fatalf("duplicate transaction error = %v", err)
	}
}

func TestCompactPreservesBalancesAndBoundsRuntimeJournal(t *testing.T) {
	t.Parallel()

	book := ledger.New()
	if err := book.FundVirtual("fund-1", "fund:alice", "alice", "USDT", 10_000); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		if err := book.Hold(
			"hold-"+strconv.Itoa(index),
			"order",
			"alice",
			"USDT",
			10,
		); err != nil {
			t.Fatal(err)
		}
		if err := book.Release(
			"release-"+strconv.Itoa(index),
			"order",
			"alice",
			"USDT",
			10,
		); err != nil {
			t.Fatal(err)
		}
	}
	beforeAvailable, beforeHeld := book.UserBalance("alice", "USDT")
	beforeTreasury := book.Balance(ledger.SystemTreasury("USDT"), "USDT")
	if err := book.Compact(); err != nil {
		t.Fatal(err)
	}
	afterAvailable, afterHeld := book.UserBalance("alice", "USDT")
	afterTreasury := book.Balance(ledger.SystemTreasury("USDT"), "USDT")
	if afterAvailable != beforeAvailable || afterHeld != beforeHeld ||
		afterTreasury != beforeTreasury {
		t.Fatalf(
			"balances changed: user before=%d/%d after=%d/%d treasury before=%d after=%d",
			beforeAvailable,
			beforeHeld,
			afterAvailable,
			afterHeld,
			beforeTreasury,
			afterTreasury,
		)
	}
	if book.JournalLen() != 1 {
		t.Fatalf("compacted journal length = %d, want 1 asset checkpoint", book.JournalLen())
	}
	if err := book.Validate(); err != nil {
		t.Fatal(err)
	}
}
