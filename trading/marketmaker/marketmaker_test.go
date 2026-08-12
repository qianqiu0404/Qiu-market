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
	tracker := marketmaker.NewStatusTracker(true)
	makerConfig := marketmaker.DefaultConfig()
	makerConfig.Status = tracker
	maker, err := marketmaker.New(
		domain.DefaultBTCUSDTMarket(),
		runner,
		source,
		makerConfig,
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
	active := tracker.Status()
	if active.State != marketmaker.LiquidityActive || !active.SubmitEnabled() ||
		active.BidLevels != 3 || active.AskLevels != 3 || active.ReferenceObservedAt.IsZero() {
		t.Fatalf("active liquidity status = %+v", active)
	}
	assertQuote(t, orders, domain.SideBuy, 59_940_000_000)
	assertQuote(t, orders, domain.SideSell, 60_060_000_000)
	assertQuote(t, orders, domain.SideBuy, 59_850_000_000)
	assertQuote(t, orders, domain.SideSell, 60_150_000_000)
	assertQuote(t, orders, domain.SideBuy, 59_700_000_000)
	assertQuote(t, orders, domain.SideSell, 60_300_000_000)

	sequenceBeforeSmallMove := runner.Status().Sequence
	source.reference = marketmaker.Reference{
		Price:      60_030_000_000,
		ObservedAt: time.Now(),
	}
	if err := maker.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.Status().Sequence != sequenceBeforeSmallMove {
		t.Fatalf("small reference movement churned quotes: sequence=%d want=%d",
			runner.Status().Sequence, sequenceBeforeSmallMove)
	}

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
	paused := tracker.Status()
	if paused.State != marketmaker.LiquidityPaused || paused.SubmitEnabled() || paused.Reason == "" ||
		paused.BidLevels != 0 || paused.AskLevels != 0 {
		t.Fatalf("paused liquidity status = %+v", paused)
	}

	source.reference = marketmaker.Reference{
		Price:      60_000_000_000,
		ObservedAt: time.Now().Add(-31 * time.Second),
	}
	if err := maker.Refresh(context.Background()); !errors.Is(err, marketmaker.ErrUnsafeReference) {
		t.Fatalf("stale error = %v", err)
	}
	for sample := 1; sample <= 3; sample++ {
		source.reference = marketmaker.Reference{
			Price:      60_000_000_000 + int64(sample),
			ObservedAt: time.Now(),
		}
		if err := maker.Refresh(context.Background()); err != nil {
			t.Fatalf("recovery sample %d: %v", sample, err)
		}
		orders, err = runner.Orders("system:demo-maker", true)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if sample == 3 {
			want = 6
		}
		if len(orders) != want {
			t.Fatalf("recovery sample %d orders = %d, want %d", sample, len(orders), want)
		}
	}
	if recovered := tracker.Status(); recovered.State != marketmaker.LiquidityActive ||
		recovered.BidLevels != 3 || recovered.AskLevels != 3 {
		t.Fatalf("recovered liquidity status = %+v", recovered)
	}
}

func TestVirtualLiquidityStatusStartsDisabledOrRecovering(t *testing.T) {
	t.Parallel()
	disabled := marketmaker.NewStatusTracker(false).Status()
	if disabled.State != marketmaker.LiquidityDisabled || disabled.Provider != "Qiu Virtual Liquidity" ||
		disabled.SubmitEnabled() {
		t.Fatalf("disabled status = %+v", disabled)
	}
	recovering := marketmaker.NewStatusTracker(true).Status()
	if recovering.State != marketmaker.LiquidityRecovering || recovering.Reason == "" ||
		recovering.SubmitEnabled() {
		t.Fatalf("recovering status = %+v", recovering)
	}
}

func TestDemoMakerRunSurvivesUserRemainderAndReplenishesAfterCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(ctx, domain.DefaultBTCUSDTMarket(), memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("maker did not stop before runner close")
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if err := runner.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	for _, fund := range []domain.FundRequest{
		{RequestID: "maker-btc", AccountID: "system:demo-maker", Asset: "BTC", Amount: 1_000_000_000},
		{RequestID: "maker-usdt", AccountID: "system:demo-maker", Asset: "USDT", Amount: 1_000_000_000_000},
		{RequestID: "user-usdt", AccountID: "user", Asset: "USDT", Amount: 10_000_000_000},
	} {
		if _, err := runner.Fund(ctx, fund); err != nil {
			t.Fatal(err)
		}
	}
	tracker := marketmaker.NewStatusTracker(true)
	config := marketmaker.DefaultConfig()
	config.Status = tracker
	config.RefreshEvery = 10 * time.Millisecond
	maker, err := marketmaker.New(domain.DefaultBTCUSDTMarket(), runner, &referenceSource{reference: marketmaker.Reference{
		Price: 60_000_000_000, ObservedAt: time.Now(),
	}}, config)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- maker.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		orders, _ := runner.Orders("system:demo-maker", true)
		return len(orders) == 6 && tracker.Status().State == marketmaker.LiquidityActive
	})
	userOrder, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "user-buy-through-three-levels", AccountID: "user",
		Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 60_300_000_000, Quantity: 4_000_000,
	})
	if err != nil || userOrder.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("user partial=%+v err=%v", userOrder, err)
	}
	waitFor(t, time.Second, func() bool {
		return tracker.Status().State == marketmaker.LiquidityRecovering
	})
	blocked := tracker.Status()
	if blocked.ReferenceObservedAt.IsZero() || blocked.LastRefreshAt.IsZero() {
		t.Fatalf("blocked maker lost reference freshness: %+v", blocked)
	}
	assertMakerRemainsRecoveringWithoutOneSidedQuotes(t, runner, tracker, done, 30*time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("maker terminated while a user remainder blocked post-only quotes: %v", err)
	default:
	}
	if _, err := runner.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel-user-remainder", AccountID: "user", OrderID: userOrder.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		orders, _ := runner.Orders("system:demo-maker", true)
		return len(orders) == 6 && tracker.Status().State == marketmaker.LiquidityActive
	})
}

func TestDemoMakerRunSurvivesSellRemainderAndReplenishesAfterCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(ctx, domain.DefaultBTCUSDTMarket(), memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("maker did not stop before runner close")
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if err := runner.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	for _, fund := range []domain.FundRequest{
		{RequestID: "maker-btc", AccountID: "system:demo-maker", Asset: "BTC", Amount: 1_000_000_000},
		{RequestID: "maker-usdt", AccountID: "system:demo-maker", Asset: "USDT", Amount: 1_000_000_000_000},
		{RequestID: "user-btc", AccountID: "user", Asset: "BTC", Amount: 10_000_000},
	} {
		if _, err := runner.Fund(ctx, fund); err != nil {
			t.Fatal(err)
		}
	}
	tracker := marketmaker.NewStatusTracker(true)
	config := marketmaker.DefaultConfig()
	config.Status = tracker
	config.RefreshEvery = 10 * time.Millisecond
	maker, err := marketmaker.New(domain.DefaultBTCUSDTMarket(), runner, &referenceSource{reference: marketmaker.Reference{
		Price: 60_000_000_000, ObservedAt: time.Now(),
	}}, config)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- maker.Run(ctx) }()
	waitFor(t, time.Second, func() bool {
		orders, _ := runner.Orders("system:demo-maker", true)
		return len(orders) == 6 && tracker.Status().State == marketmaker.LiquidityActive
	})
	userOrder, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "user-sell-through-three-levels", AccountID: "user",
		Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 59_700_000_000, Quantity: 4_000_000,
	})
	if err != nil || userOrder.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("user partial=%+v err=%v", userOrder, err)
	}
	waitFor(t, time.Second, func() bool {
		return tracker.Status().State == marketmaker.LiquidityRecovering
	})
	blocked := tracker.Status()
	if blocked.ReferenceObservedAt.IsZero() || blocked.LastRefreshAt.IsZero() {
		t.Fatalf("blocked maker lost reference freshness: %+v", blocked)
	}
	assertMakerRemainsRecoveringWithoutOneSidedQuotes(t, runner, tracker, done, 30*time.Millisecond)
	if _, err := runner.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel-user-sell-remainder", AccountID: "user", OrderID: userOrder.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		orders, _ := runner.Orders("system:demo-maker", true)
		return len(orders) == 6 && tracker.Status().State == marketmaker.LiquidityActive
	})
}

func assertMakerRemainsRecoveringWithoutOneSidedQuotes(
	t *testing.T,
	runner *tradingruntime.MarketRunner,
	tracker *marketmaker.StatusTracker,
	done <-chan error,
	duration time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		orders, err := runner.Orders("system:demo-maker", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(orders) != 0 {
			t.Fatalf("blocked maker left a partial/one-sided quote set: %+v", orders)
		}
		if tracker.Status().State != marketmaker.LiquidityRecovering {
			t.Fatalf("blocked maker status = %+v", tracker.Status())
		}
		select {
		case err := <-done:
			t.Fatalf("maker terminated while a user remainder blocked post-only quotes: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
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

func waitFor(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
