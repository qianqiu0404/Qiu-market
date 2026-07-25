package marketmaker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestDemoMakerQuotesThreeLevelsAndStopsOnStaleOrJump(t *testing.T) {
	t.Parallel()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		context.Background(),
		domain.DefaultBTCUSDTMarket(),
		memory,
		memory,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runner.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	if _, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "maker-btc",
		AccountID: "system:demo-maker",
		Asset:     "BTC",
		Amount:    100_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Fund(context.Background(), domain.FundRequest{
		RequestID: "maker-usdt",
		AccountID: "system:demo-maker",
		Asset:     "USDT",
		Amount:    100_000_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	source := &referenceSource{reference: marketmaker.Reference{
		Price:      60_000_000_000,
		ObservedAt: time.Now(),
	}}
	maker, err := marketmaker.New(
		domain.DefaultBTCUSDTMarket(),
		runner,
		source,
		marketmaker.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := maker.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	orders, err := runner.Orders("system:demo-maker", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 6 {
		t.Fatalf("open demo-maker orders = %d, want 6", len(orders))
	}
	assertQuote(t, orders, domain.SideBuy, 59_940_000_000)
	assertQuote(t, orders, domain.SideSell, 60_060_000_000)
	assertQuote(t, orders, domain.SideBuy, 59_850_000_000)
	assertQuote(t, orders, domain.SideSell, 60_150_000_000)
	assertQuote(t, orders, domain.SideBuy, 59_700_000_000)
	assertQuote(t, orders, domain.SideSell, 60_300_000_000)

	source.reference = marketmaker.Reference{
		Price:      60_600_000_000,
		ObservedAt: time.Now(),
	}
	if err := maker.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	orders, err = runner.Orders("system:demo-maker", true)
	if err != nil || len(orders) != 6 {
		t.Fatalf("requote orders = %d, %v", len(orders), err)
	}

	source.reference = marketmaker.Reference{
		Price:      70_000_000_000,
		ObservedAt: time.Now(),
	}
	if err := maker.Refresh(context.Background()); !errors.Is(err, marketmaker.ErrUnsafeReference) {
		t.Fatalf("jump error = %v", err)
	}
	orders, err = runner.Orders("system:demo-maker", true)
	if err != nil || len(orders) != 0 {
		t.Fatalf("orders after unsafe jump = %d, %v", len(orders), err)
	}

	source.reference = marketmaker.Reference{
		Price:      60_000_000_000,
		ObservedAt: time.Now().Add(-31 * time.Second),
	}
	if err := maker.Refresh(context.Background()); !errors.Is(err, marketmaker.ErrUnsafeReference) {
		t.Fatalf("stale error = %v", err)
	}
}

type referenceSource struct {
	reference marketmaker.Reference
	err       error
}

func (s *referenceSource) Current(context.Context) (marketmaker.Reference, error) {
	return s.reference, s.err
}

func assertQuote(t *testing.T, orders []domain.Order, side domain.Side, price int64) {
	t.Helper()
	for _, order := range orders {
		if order.Side == side && order.Price == price && order.PostOnly &&
			order.OriginalQuantity == 1_000_000 {
			return
		}
	}
	t.Fatalf("quote side=%s price=%d not found: %+v", side, price, orders)
}
