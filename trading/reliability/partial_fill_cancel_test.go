package reliability_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

const (
	makerQuantity   int64 = 10_000
	partialQuantity int64 = 4_000
	tradePrice      int64 = 100
	buyerFunding    int64 = 1_000_000
)

func TestPartialFillThenCancelAccounting(t *testing.T) {
	runner, persistence := newRunner(t)
	fundRunner(t, runner, "fund-seller", "seller", "BTC", makerQuantity)
	fundRunner(t, runner, "fund-buyer", "buyer", "USDT", buyerFunding)
	maker := submitMakerSell(t, runner, "maker-partial")

	taker, err := runner.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "taker-partial",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         tradePrice,
		Quantity:      partialQuantity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taker.Status != domain.OrderStatusFilled {
		t.Fatalf("partial taker status = %s", taker.Status)
	}

	beforeCancel := mustRunnerOrder(t, runner, maker.OrderID)
	assertOrderQuantities(t, beforeCancel, makerQuantity, partialQuantity)
	if beforeCancel.Status != domain.OrderStatusPartiallyFilled ||
		beforeCancel.HeldAsset != "BTC" ||
		beforeCancel.HeldAmount != makerQuantity-partialQuantity {
		t.Fatalf("partially filled maker = %+v", beforeCancel)
	}
	assertRunnerBalance(t, runner, "seller", "BTC", 0, makerQuantity-partialQuantity)

	canceled, err := runner.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-partial-remainder",
		AccountID: "seller",
		OrderID:   maker.OrderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.OrderStatusCanceled {
		t.Fatalf("partial cancel result = %+v", canceled)
	}

	final := mustRunnerOrder(t, runner, maker.OrderID)
	assertOrderQuantities(t, final, makerQuantity, partialQuantity)
	if final.Status != domain.OrderStatusCanceled ||
		final.HeldAsset != "" ||
		final.HeldAmount != 0 {
		t.Fatalf("canceled partial maker = %+v", final)
	}
	assertSettlement(
		t,
		runner,
		persistence,
		partialQuantity,
		makerQuantity-partialQuantity,
	)
}

func TestFullFillThenLateCancelAccounting(t *testing.T) {
	runner, persistence := newRunner(t)
	fundRunner(t, runner, "fund-seller", "seller", "BTC", makerQuantity)
	fundRunner(t, runner, "fund-buyer", "buyer", "USDT", buyerFunding)
	maker := submitMakerSell(t, runner, "maker-full")

	taker, err := runner.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "taker-full",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         tradePrice,
		Quantity:      makerQuantity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taker.Status != domain.OrderStatusFilled {
		t.Fatalf("full taker status = %s", taker.Status)
	}

	filled := mustRunnerOrder(t, runner, maker.OrderID)
	assertOrderQuantities(t, filled, makerQuantity, makerQuantity)
	if filled.Status != domain.OrderStatusFilled ||
		filled.HeldAsset != "" ||
		filled.HeldAmount != 0 {
		t.Fatalf("fully filled maker = %+v", filled)
	}

	lateCancel, err := runner.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "late-cancel-filled",
		AccountID: "seller",
		OrderID:   maker.OrderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lateCancel.Status != domain.OrderStatusRejected ||
		len(lateCancel.Events) != 1 ||
		lateCancel.Events[0].Type != domain.EventCancelRejected {
		t.Fatalf("late cancel result = %+v", lateCancel)
	}
	afterCancel := mustRunnerOrder(t, runner, maker.OrderID)
	if afterCancel != filled {
		t.Fatalf("late cancel changed filled order: before=%+v after=%+v", filled, afterCancel)
	}
	records := recordsFrom(t, persistence)
	if len(records) != 5 || len(records[len(records)-1].Journal) != 0 {
		t.Fatalf("late cancel records/journal = %d/%+v", len(records), records[len(records)-1].Journal)
	}
	assertSettlement(t, runner, persistence, makerQuantity, 0)
}

func TestConcurrentFillCancelLinearizesWithoutDoubleUnlock(t *testing.T) {
	const iterations = 32
	var outcomes struct {
		sync.Mutex
		cancelFirst int
		fillFirst   int
	}

	for iteration := 0; iteration < iterations; iteration++ {
		runner, persistence := createRunner(t)
		fundRunner(t, runner, fmt.Sprintf("fund-seller-%d", iteration), "seller", "BTC", makerQuantity)
		fundRunner(t, runner, fmt.Sprintf("fund-buyer-%d", iteration), "buyer", "USDT", buyerFunding)
		maker := submitMakerSell(t, runner, fmt.Sprintf("maker-race-%d", iteration))

		type commandResult struct {
			result domain.Result
			err    error
		}
		start := make(chan struct{})
		fillDone := make(chan commandResult, 1)
		cancelDone := make(chan commandResult, 1)
		go func() {
			<-start
			result, err := runner.Submit(context.Background(), domain.NewOrder{
				ClientOrderID: fmt.Sprintf("taker-race-%d", iteration),
				AccountID:     "buyer",
				Side:          domain.SideBuy,
				Type:          domain.OrderTypeLimit,
				TimeInForce:   domain.TimeInForceIOC,
				Price:         tradePrice,
				Quantity:      partialQuantity,
			})
			fillDone <- commandResult{result: result, err: err}
		}()
		go func() {
			<-start
			result, err := runner.Cancel(context.Background(), domain.CancelOrder{
				RequestID: fmt.Sprintf("cancel-race-%d", iteration),
				AccountID: "seller",
				OrderID:   maker.OrderID,
			})
			cancelDone <- commandResult{result: result, err: err}
		}()
		close(start)
		fill := <-fillDone
		cancel := <-cancelDone
		if fill.err != nil || cancel.err != nil {
			closeRunnerNow(t, runner)
			t.Fatalf("iteration %d concurrent fill/cancel errors = %v / %v",
				iteration, fill.err, cancel.err)
		}
		if cancel.result.Status != domain.OrderStatusCanceled {
			closeRunnerNow(t, runner)
			t.Fatalf("iteration %d cancel result = %+v", iteration, cancel.result)
		}

		final := mustRunnerOrder(t, runner, maker.OrderID)
		if final.Status != domain.OrderStatusCanceled ||
			final.HeldAsset != "" ||
			final.HeldAmount != 0 {
			closeRunnerNow(t, runner)
			t.Fatalf("iteration %d final maker = %+v", iteration, final)
		}
		switch final.FilledQuantity {
		case 0:
			outcomes.Lock()
			outcomes.cancelFirst++
			outcomes.Unlock()
			if fill.result.Status != domain.OrderStatusCanceled {
				closeRunnerNow(t, runner)
				t.Fatalf("iteration %d cancel-first taker = %+v", iteration, fill.result)
			}
		case partialQuantity:
			outcomes.Lock()
			outcomes.fillFirst++
			outcomes.Unlock()
			if fill.result.Status != domain.OrderStatusFilled {
				closeRunnerNow(t, runner)
				t.Fatalf("iteration %d fill-first taker = %+v", iteration, fill.result)
			}
		default:
			closeRunnerNow(t, runner)
			t.Fatalf("iteration %d unexpected filled quantity = %d", iteration, final.FilledQuantity)
		}
		assertOrderQuantities(t, final, makerQuantity, final.FilledQuantity)
		assertSettlement(
			t,
			runner,
			persistence,
			final.FilledQuantity,
			makerQuantity-final.FilledQuantity,
		)
		if status := runner.Status(); status.Sequence != 5 || status.State != tradingruntime.StateReady {
			closeRunnerNow(t, runner)
			t.Fatalf("iteration %d runner status = %+v", iteration, status)
		}
		closeRunnerNow(t, runner)
	}

	t.Logf("valid serializations: fill-first=%d cancel-first=%d",
		outcomes.fillFirst, outcomes.cancelFirst)
}

func submitMakerSell(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	clientOrderID string,
) domain.Result {
	t.Helper()
	result, err := runner.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: clientOrderID,
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
	if result.Status != domain.OrderStatusOpen {
		t.Fatalf("maker submit result = %+v", result)
	}
	return result
}

func mustRunnerOrder(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	orderID domain.OrderID,
) domain.Order {
	t.Helper()
	order, found, err := runner.Order(orderID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("order %s not found", orderID)
	}
	return order
}

func assertOrderQuantities(
	t *testing.T,
	order domain.Order,
	original, filled int64,
) {
	t.Helper()
	if order.OriginalQuantity != original ||
		order.FilledQuantity != filled ||
		order.RemainingQuantity != original-filled ||
		order.FilledQuantity+order.RemainingQuantity != order.OriginalQuantity {
		t.Fatalf("order quantity invariant failed: %+v", order)
	}
}

func assertSettlement(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	persistence *store.Memory,
	filled, sellerUnlocked int64,
) {
	t.Helper()
	notional := filled * tradePrice
	buyerFee := filled * reliabilityMarket().TakerFeeBPS / 10_000
	sellerFee := notional * reliabilityMarket().MakerFeeBPS / 10_000

	assertRunnerBalance(t, runner, "seller", "BTC", sellerUnlocked, 0)
	assertRunnerBalance(t, runner, "seller", "USDT", notional-sellerFee, 0)
	assertRunnerBalance(t, runner, "buyer", "BTC", filled-buyerFee, 0)
	assertRunnerBalance(t, runner, "buyer", "USDT", buyerFunding-notional, 0)

	records := recordsFrom(t, persistence)
	assertJournalsBalanced(t, records)
	if got := ledgerAccountTotal(records, ledger.PlatformFee("BTC"), "BTC"); got != buyerFee {
		t.Fatalf("BTC platform fee = %d, want %d", got, buyerFee)
	}
	if got := ledgerAccountTotal(records, ledger.PlatformFee("USDT"), "USDT"); got != sellerFee {
		t.Fatalf("USDT platform fee = %d, want %d", got, sellerFee)
	}
}

func assertRunnerBalance(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	accountID domain.AccountID,
	asset domain.Asset,
	available, held int64,
) {
	t.Helper()
	balance, err := runner.Balance(accountID, asset)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Available != available || balance.Held != held {
		t.Fatalf("%s/%s balance = %+v, want available=%d held=%d",
			accountID, asset, balance, available, held)
	}
}

func recordsFrom(t *testing.T, persistence *store.Memory) []store.Record {
	t.Helper()
	records, err := persistence.RecordsAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func assertJournalsBalanced(t *testing.T, records []store.Record) {
	t.Helper()
	for _, record := range records {
		for _, transaction := range record.Journal {
			totals := make(map[domain.Asset]int64)
			for _, entry := range transaction.Entries {
				next, err := domain.CheckedAdd(totals[entry.Asset], entry.Amount)
				if err != nil {
					t.Fatalf("transaction %s overflow: %v", transaction.ID, err)
				}
				totals[entry.Asset] = next
			}
			for asset, total := range totals {
				if total != 0 {
					t.Fatalf("transaction %s/%s total = %d", transaction.ID, asset, total)
				}
			}
		}
	}
}

func ledgerAccountTotal(
	records []store.Record,
	account string,
	asset domain.Asset,
) int64 {
	var total int64
	for _, record := range records {
		for _, transaction := range record.Journal {
			for _, entry := range transaction.Entries {
				if entry.Account == account && entry.Asset == asset {
					total += entry.Amount
				}
			}
		}
	}
	return total
}

func createRunner(t *testing.T) (*tradingruntime.MarketRunner, *store.Memory) {
	t.Helper()
	persistence := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
		tradingruntime.Config{
			QueueSize:       16,
			SnapshotEvery:   100,
			SnapshotTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return runner, persistence
}

func closeRunnerNow(t *testing.T, runner *tradingruntime.MarketRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Close(ctx); err != nil {
		t.Fatalf("close runner: %v", err)
	}
}
