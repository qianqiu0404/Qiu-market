package reliability_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

var errResponseLostAfterCommit = errors.New("simulated response loss after committed event")

func TestSubmittedUnknownReusesOriginalIdentity(t *testing.T) {
	t.Run("fund queries balance and replays the same request id", func(t *testing.T) {
		runner, persistence := newRunner(t)
		request := domain.FundRequest{
			RequestID: "fund-response-lost",
			AccountID: "alice",
			Asset:     "USDT",
			Amount:    20_000,
		}

		err := executeAndLoseResponse(func() (domain.Result, error) {
			return runner.Fund(context.Background(), request)
		})
		if !errors.Is(err, errResponseLostAfterCommit) {
			t.Fatalf("lost fund response error = %v", err)
		}

		balance, err := runner.Balance(request.AccountID, request.Asset)
		if err != nil {
			t.Fatal(err)
		}
		if balance.Available != request.Amount || balance.Held != 0 {
			t.Fatalf("authoritative funded balance = %+v", balance)
		}
		replayed, err := runner.Fund(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Sequence != 1 || persistence.RecordCount() != 1 {
			t.Fatalf("same-id fund replay sequence/records = %d/%d, want 1/1",
				replayed.Sequence, persistence.RecordCount())
		}

		conflict := request
		conflict.Amount++
		if _, err := runner.Fund(context.Background(), conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Fatalf("changed fund payload error = %v", err)
		}
	})

	t.Run("submit queries order and replays the same client order id", func(t *testing.T) {
		runner, persistence := newRunner(t)
		fundRunner(t, runner, "fund-buyer", "buyer", "USDT", 20_000)
		request := domain.NewOrder{
			ClientOrderID: "submit-response-lost",
			AccountID:     "buyer",
			Side:          domain.SideBuy,
			Type:          domain.OrderTypeLimit,
			TimeInForce:   domain.TimeInForceGTC,
			Price:         100,
			Quantity:      100,
		}

		err := executeAndLoseResponse(func() (domain.Result, error) {
			return runner.Submit(context.Background(), request)
		})
		if !errors.Is(err, errResponseLostAfterCommit) {
			t.Fatalf("lost submit response error = %v", err)
		}

		orders, err := runner.Orders(request.AccountID, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(orders) != 1 || orders[0].ClientOrderID != request.ClientOrderID ||
			orders[0].Status != domain.OrderStatusOpen ||
			orders[0].FilledQuantity != 0 ||
			orders[0].RemainingQuantity != request.Quantity ||
			orders[0].HeldAsset != "USDT" ||
			orders[0].HeldAmount != 10_000 {
			t.Fatalf("authoritative submitted order = %+v", orders)
		}
		balance, err := runner.Balance(request.AccountID, "USDT")
		if err != nil {
			t.Fatal(err)
		}
		if balance.Available != 10_000 || balance.Held != 10_000 {
			t.Fatalf("submitted order balance = %+v", balance)
		}

		replayed, err := runner.Submit(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Sequence != 2 || replayed.OrderID != orders[0].ID ||
			persistence.RecordCount() != 2 {
			t.Fatalf("same-id submit replay = %+v records=%d",
				replayed, persistence.RecordCount())
		}

		conflict := request
		conflict.Price++
		if _, err := runner.Submit(context.Background(), conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Fatalf("changed submit payload error = %v", err)
		}
	})

	t.Run("cancel queries terminal order and replays the same request id", func(t *testing.T) {
		runner, persistence := newRunner(t)
		fundRunner(t, runner, "fund-buyer", "buyer", "USDT", 20_000)
		submitted, err := runner.Submit(context.Background(), domain.NewOrder{
			ClientOrderID: "resting-order",
			AccountID:     "buyer",
			Side:          domain.SideBuy,
			Type:          domain.OrderTypeLimit,
			TimeInForce:   domain.TimeInForceGTC,
			Price:         100,
			Quantity:      100,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := domain.CancelOrder{
			RequestID: "cancel-response-lost",
			AccountID: "buyer",
			OrderID:   submitted.OrderID,
		}

		err = executeAndLoseResponse(func() (domain.Result, error) {
			return runner.Cancel(context.Background(), request)
		})
		if !errors.Is(err, errResponseLostAfterCommit) {
			t.Fatalf("lost cancel response error = %v", err)
		}

		order, found, err := runner.Order(submitted.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		if !found || order.Status != domain.OrderStatusCanceled ||
			order.HeldAsset != "" || order.HeldAmount != 0 {
			t.Fatalf("authoritative canceled order = found=%t %+v", found, order)
		}
		balance, err := runner.Balance(request.AccountID, "USDT")
		if err != nil {
			t.Fatal(err)
		}
		if balance.Available != 20_000 || balance.Held != 0 {
			t.Fatalf("canceled order balance = %+v", balance)
		}

		replayed, err := runner.Cancel(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if replayed.Sequence != 3 || replayed.Status != domain.OrderStatusCanceled ||
			persistence.RecordCount() != 3 {
			t.Fatalf("same-id cancel replay = %+v records=%d",
				replayed, persistence.RecordCount())
		}
		assertSingleCancelRelease(t, persistence, submitted.OrderID)

		conflict := request
		conflict.OrderID = "another-order"
		if _, err := runner.Cancel(context.Background(), conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Fatalf("changed cancel payload error = %v", err)
		}
	})
}

func executeAndLoseResponse(execute func() (domain.Result, error)) error {
	result, err := execute()
	if err != nil {
		return err
	}
	if result.Sequence == 0 {
		return fmt.Errorf("committed result has no sequence")
	}
	return errResponseLostAfterCommit
}

func assertSingleCancelRelease(
	t *testing.T,
	persistence *store.Memory,
	orderID domain.OrderID,
) {
	t.Helper()
	records, err := persistence.RecordsAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	reference := "order-cancel:" + string(orderID)
	var releases int
	for _, record := range records {
		for _, transaction := range record.Journal {
			if transaction.Reference == reference {
				releases++
			}
		}
	}
	if releases != 1 {
		t.Fatalf("cancel release transactions = %d, want 1", releases)
	}
}

func newRunner(t *testing.T) (*tradingruntime.MarketRunner, *store.Memory) {
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runner.Close(ctx); err != nil {
			t.Errorf("close market runner: %v", err)
		}
	})
	return runner, persistence
}

func reliabilityMarket() domain.Market {
	return domain.Market{
		ID:                 "BTC-USDT",
		BaseAsset:          "BTC",
		QuoteAsset:         "USDT",
		BaseScale:          1,
		QuoteScale:         1,
		PriceTick:          1,
		QuantityStep:       1,
		MinQuantity:        1,
		MinNotional:        1,
		MakerFeeBPS:        10,
		TakerFeeBPS:        20,
		ConfigurationEpoch: 1,
	}
}

func fundRunner(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	requestID string,
	accountID domain.AccountID,
	asset domain.Asset,
	amount int64,
) {
	t.Helper()
	if _, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: requestID,
		AccountID: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}
