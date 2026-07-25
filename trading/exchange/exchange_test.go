package exchange_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestPriceTimeSettlementFeesAndCancel(t *testing.T) {
	t.Parallel()

	ex, _ := mustExchange(t)
	fund(t, ex, "fund-seller-1", "seller-1", "BTC", 1_000)
	fund(t, ex, "fund-seller-2", "seller-2", "BTC", 1_000)
	fund(t, ex, "fund-buyer", "buyer", "USDT", 100_000)

	first := submit(t, ex, domain.NewOrder{
		ClientOrderID: "sell-1",
		AccountID:     "seller-1",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      500,
	})
	second := submit(t, ex, domain.NewOrder{
		ClientOrderID: "sell-2",
		AccountID:     "seller-2",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      500,
	})
	buy := submit(t, ex, domain.NewOrder{
		ClientOrderID: "buy-1",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         110,
		Quantity:      750,
	})

	trades := tradesFrom(buy)
	if len(trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(trades))
	}
	if trades[0].MakerOrderID != first.OrderID || trades[0].Quantity != 500 ||
		trades[1].MakerOrderID != second.OrderID || trades[1].Quantity != 250 {
		t.Fatalf("price-time fills = %+v", trades)
	}
	if buy.Status != domain.OrderStatusFilled {
		t.Fatalf("buy status = %s", buy.Status)
	}

	assertBalance(t, ex, "buyer", "USDT", 25_000, 0)
	assertBalance(t, ex, "buyer", "BTC", 749, 0)
	assertBalance(t, ex, "seller-1", "USDT", 49_950, 0)
	assertBalance(t, ex, "seller-2", "USDT", 24_975, 0)
	if got := ex.PlatformFees("BTC"); got != 1 {
		t.Fatalf("BTC fees = %d, want 1", got)
	}
	if got := ex.PlatformFees("USDT"); got != 75 {
		t.Fatalf("USDT fees = %d, want 75", got)
	}

	order, ok := ex.Order(second.OrderID)
	if !ok || order.Status != domain.OrderStatusPartiallyFilled || order.RemainingQuantity != 250 {
		t.Fatalf("second maker after partial fill = %+v, exists=%t", order, ok)
	}
	cancel, err := ex.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-sell-2",
		AccountID: "seller-2",
		OrderID:   second.OrderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancel.Status != domain.OrderStatusCanceled {
		t.Fatalf("cancel status = %s", cancel.Status)
	}
	assertBalance(t, ex, "seller-2", "BTC", 750, 0)

	if err := ex.Validate(); err != nil {
		t.Fatalf("exchange validation: %v", err)
	}
	assertJournalBalanced(t, ex)
}

func TestPostOnlyFOKAndMarketBuy(t *testing.T) {
	t.Parallel()

	ex, _ := mustExchange(t)
	fund(t, ex, "fund-seller", "seller", "BTC", 1_000)
	fund(t, ex, "fund-buyer", "buyer", "USDT", 20_000)
	sell := submit(t, ex, domain.NewOrder{
		ClientOrderID: "sell",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})

	postOnly := submit(t, ex, domain.NewOrder{
		ClientOrderID: "post-only",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      10,
		PostOnly:      true,
	})
	if postOnly.Status != domain.OrderStatusRejected || postOnly.Events[0].Reason != "post_only_would_cross" {
		t.Fatalf("post-only result = %+v", postOnly)
	}
	assertBalance(t, ex, "buyer", "USDT", 20_000, 0)

	fokRejected := submit(t, ex, domain.NewOrder{
		ClientOrderID: "fok-rejected",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceFOK,
		Price:         100,
		Quantity:      101,
	})
	if fokRejected.Status != domain.OrderStatusRejected || fokRejected.Events[0].Reason != "fok_not_fillable" {
		t.Fatalf("FOK rejection = %+v", fokRejected)
	}

	marketBuy := submit(t, ex, domain.NewOrder{
		ClientOrderID: "market-buy",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		TimeInForce:   domain.TimeInForceIOC,
		QuoteBudget:   550,
	})
	if marketBuy.Status != domain.OrderStatusFilled {
		t.Fatalf("market buy status = %s", marketBuy.Status)
	}
	trades := tradesFrom(marketBuy)
	if len(trades) != 1 || trades[0].Quantity != 5 || trades[0].QuoteAmount != 500 {
		t.Fatalf("market buy trades = %+v", trades)
	}
	assertBalance(t, ex, "buyer", "USDT", 19_500, 0)

	resting, ok := ex.Order(sell.OrderID)
	if !ok || resting.RemainingQuantity != 95 {
		t.Fatalf("resting sell = %+v", resting)
	}
}

func TestGTCRemainderKeepsOnlyWorstCaseHold(t *testing.T) {
	t.Parallel()

	ex, _ := mustExchange(t)
	fund(t, ex, "fund-seller", "seller", "BTC", 50)
	fund(t, ex, "fund-buyer", "buyer", "USDT", 10_000)
	submit(t, ex, domain.NewOrder{
		ClientOrderID: "better-ask",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         90,
		Quantity:      50,
	})
	buy := submit(t, ex, domain.NewOrder{
		ClientOrderID: "resting-bid",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})
	if buy.Status != domain.OrderStatusPartiallyFilled {
		t.Fatalf("buy status = %s", buy.Status)
	}
	assertBalance(t, ex, "buyer", "USDT", 500, 5_000)
	order, ok := ex.Order(buy.OrderID)
	if !ok || order.RemainingQuantity != 50 {
		t.Fatalf("resting buyer order = %+v", order)
	}
	if _, err := ex.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-bid",
		AccountID: "buyer",
		OrderID:   buy.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	assertBalance(t, ex, "buyer", "USDT", 5_500, 0)
}

func TestFOKSuccessAndMarketSell(t *testing.T) {
	t.Parallel()

	ex, _ := mustExchange(t)
	fund(t, ex, "fund-seller", "seller", "BTC", 2_000)
	fund(t, ex, "fund-buyer", "buyer", "USDT", 200_000)
	submit(t, ex, domain.NewOrder{
		ClientOrderID: "maker-ask",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      1_000,
	})
	fok := submit(t, ex, domain.NewOrder{
		ClientOrderID: "fok-buy",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceFOK,
		Price:         100,
		Quantity:      1_000,
	})
	if fok.Status != domain.OrderStatusFilled || len(tradesFrom(fok)) != 1 {
		t.Fatalf("successful FOK = %+v", fok)
	}

	submit(t, ex, domain.NewOrder{
		ClientOrderID: "maker-bid",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         90,
		Quantity:      500,
	})
	marketSell := submit(t, ex, domain.NewOrder{
		ClientOrderID: "market-sell",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeMarket,
		TimeInForce:   domain.TimeInForceIOC,
		Quantity:      500,
	})
	if marketSell.Status != domain.OrderStatusFilled || len(tradesFrom(marketSell)) != 1 {
		t.Fatalf("market sell = %+v", marketSell)
	}
	assertBalance(t, ex, "seller", "BTC", 500, 0)
	if err := ex.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSelfTradeCancelTakerAndFOKPrecheck(t *testing.T) {
	t.Parallel()

	ex, _ := mustExchange(t)
	fund(t, ex, "fund-base", "alice", "BTC", 1_000)
	fund(t, ex, "fund-quote", "alice", "USDT", 100_000)
	sell := submit(t, ex, domain.NewOrder{
		ClientOrderID: "alice-sell",
		AccountID:     "alice",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      1_000,
	})

	fok := submit(t, ex, domain.NewOrder{
		ClientOrderID: "alice-fok",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceFOK,
		Price:         100,
		Quantity:      1,
	})
	if fok.Status != domain.OrderStatusRejected || fok.Events[0].Reason != "fok_not_fillable" {
		t.Fatalf("self FOK = %+v", fok)
	}

	ioc := submit(t, ex, domain.NewOrder{
		ClientOrderID: "alice-ioc",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         100,
		Quantity:      100,
	})
	if ioc.Status != domain.OrderStatusCanceled || len(tradesFrom(ioc)) != 0 ||
		!hasEvent(ioc, domain.EventSelfTradePrevented) {
		t.Fatalf("self IOC = %+v", ioc)
	}
	assertBalance(t, ex, "alice", "USDT", 100_000, 0)
	if ex.PlatformFees("BTC") != 0 || ex.PlatformFees("USDT") != 0 {
		t.Fatal("self-trade prevention must not charge fees")
	}
	resting, ok := ex.Order(sell.OrderID)
	if !ok || resting.Status != domain.OrderStatusOpen || resting.RemainingQuantity != 1_000 {
		t.Fatalf("maker changed by STP = %+v", resting)
	}
}

func TestIdempotencyAndConcurrentRetry(t *testing.T) {
	t.Parallel()

	ex, memory := mustExchange(t)
	fundRequest := domain.FundRequest{
		RequestID: "fund-buyer",
		AccountID: "buyer",
		Asset:     "USDT",
		Amount:    100_000,
	}
	firstFund, err := ex.Fund(context.Background(), fundRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondFund, err := ex.Fund(context.Background(), fundRequest)
	if err != nil {
		t.Fatal(err)
	}
	if firstFund.Sequence != secondFund.Sequence || memory.RecordCount() != 1 {
		t.Fatalf("fund idempotency sequences %d/%d records=%d", firstFund.Sequence, secondFund.Sequence, memory.RecordCount())
	}
	conflict := fundRequest
	conflict.Amount++
	if _, err := ex.Fund(context.Background(), conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("fund idempotency conflict = %v", err)
	}

	order := domain.NewOrder{
		ClientOrderID: "concurrent-buy",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	}
	const workers = 16
	results := make(chan domain.Result, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, submitErr := ex.Submit(context.Background(), order)
			results <- result
			errs <- submitErr
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for submitErr := range errs {
		if submitErr != nil {
			t.Fatalf("concurrent submit: %v", submitErr)
		}
	}
	var sequence uint64
	for result := range results {
		if sequence == 0 {
			sequence = result.Sequence
		}
		if result.Sequence != sequence {
			t.Fatalf("concurrent result sequence = %d, want %d", result.Sequence, sequence)
		}
	}
	if memory.RecordCount() != 2 {
		t.Fatalf("records = %d, want 2", memory.RecordCount())
	}
	assertBalance(t, ex, "buyer", "USDT", 90_000, 10_000)
}

func TestInsufficientBalanceOverflowAndCancelAuthorization(t *testing.T) {
	t.Parallel()

	ex, memory := mustExchange(t)
	insufficient := submit(t, ex, domain.NewOrder{
		ClientOrderID: "no-money",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      10,
	})
	if insufficient.Status != domain.OrderStatusRejected || insufficient.Events[0].Reason != "insufficient_balance" {
		t.Fatalf("insufficient result = %+v", insufficient)
	}

	_, err := ex.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "overflow",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         math.MaxInt64,
		Quantity:      2,
	})
	if !errors.Is(err, domain.ErrInvalidOrder) {
		t.Fatalf("overflow error = %v", err)
	}
	if memory.RecordCount() != 1 {
		t.Fatalf("invalid overflow request should not be sequenced, records=%d", memory.RecordCount())
	}

	fund(t, ex, "fund-seller", "seller", "BTC", 100)
	sell := submit(t, ex, domain.NewOrder{
		ClientOrderID: "sell",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})
	unauthorized, err := ex.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-by-attacker",
		AccountID: "attacker",
		OrderID:   sell.OrderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unauthorized.Status != domain.OrderStatusRejected ||
		unauthorized.Events[0].Reason != exchange.ErrOrderOwnerMismatch.Error() {
		t.Fatalf("unauthorized cancel = %+v", unauthorized)
	}
	resting, _ := ex.Order(sell.OrderID)
	if !resting.IsOpen() {
		t.Fatal("unauthorized cancel changed the order")
	}
}

func TestSnapshotReplayAndCorruptionDetection(t *testing.T) {
	t.Parallel()

	ex, memory := mustExchange(t)
	fund(t, ex, "fund-seller", "seller", "BTC", 1_000)
	fund(t, ex, "fund-buyer", "buyer", "USDT", 200_000)
	sell := submit(t, ex, domain.NewOrder{
		ClientOrderID: "sell",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      1_000,
	})
	snapshot, err := ex.SaveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence != 3 {
		t.Fatalf("snapshot sequence = %d, want 3", snapshot.Sequence)
	}

	buyRequest := domain.NewOrder{
		ClientOrderID: "buy",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         110,
		Quantity:      400,
	}
	buy := submit(t, ex, buyRequest)
	if buy.Status != domain.OrderStatusFilled {
		t.Fatalf("buy status = %s", buy.Status)
	}
	if _, err := ex.Cancel(context.Background(), domain.CancelOrder{
		RequestID: "cancel-sell",
		AccountID: "seller",
		OrderID:   sell.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	beforeHash, err := ex.StateHash()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := exchange.Restore(context.Background(), testMarket(), memory, memory)
	if err != nil {
		t.Fatal(err)
	}
	afterHash, err := restored.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterHash {
		t.Fatalf("restored hash = %s, want %s", afterHash, beforeHash)
	}
	if restored.Sequence() != ex.Sequence() {
		t.Fatalf("restored sequence = %d, want %d", restored.Sequence(), ex.Sequence())
	}
	recordsBeforeRetry := memory.RecordCount()
	retried, err := restored.Submit(context.Background(), buyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Sequence != buy.Sequence || memory.RecordCount() != recordsBeforeRetry {
		t.Fatalf("replayed idempotency result=%d records=%d", retried.Sequence, memory.RecordCount())
	}

	corrupt, corruptMemory := mustExchange(t)
	fund(t, corrupt, "fund-1", "alice", "USDT", 100)
	if _, err := corrupt.SaveSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	fund(t, corrupt, "fund-2", "bob", "USDT", 100)
	if err := corruptMemory.CorruptRecord(2, func(record *store.Record) {
		record.StateHash = "corrupted"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.Restore(context.Background(), testMarket(), corruptMemory, corruptMemory); !errors.Is(err, exchange.ErrRecoveryDiverged) {
		t.Fatalf("corrupt recovery error = %v", err)
	}
}

func TestScopedIdempotencyAndQueryViews(t *testing.T) {
	t.Parallel()

	ex, memory := mustExchange(t)
	fund(t, ex, "shared-fund", "alice", "USDT", 20_000)
	fund(t, ex, "shared-fund", "bob", "USDT", 20_000)

	alice := submit(t, ex, domain.NewOrder{
		ClientOrderID: "shared-order",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})
	bob := submit(t, ex, domain.NewOrder{
		ClientOrderID: "shared-order",
		AccountID:     "bob",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})
	if alice.OrderID == bob.OrderID || ex.Sequence() != 4 {
		t.Fatalf("scoped order ids/sequences = %s/%s seq=%d", alice.OrderID, bob.OrderID, ex.Sequence())
	}

	retry := submit(t, ex, domain.NewOrder{
		ClientOrderID: "shared-order",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         100,
		Quantity:      100,
	})
	if retry.Sequence != alice.Sequence || ex.Sequence() != 4 {
		t.Fatalf("idempotent retry sequence=%d engine=%d, want %d/4", retry.Sequence, ex.Sequence(), alice.Sequence)
	}
	_, err := ex.Submit(context.Background(), domain.NewOrder{
		ClientOrderID: "shared-order",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         101,
		Quantity:      100,
	})
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}

	depth, err := ex.Depth(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(depth.Bids) != 1 || depth.Bids[0].Price != 100 ||
		depth.Bids[0].Quantity != 200 || depth.Bids[0].OrderCount != 2 {
		t.Fatalf("aggregated depth = %+v", depth)
	}
	if orders := ex.Orders("alice", true); len(orders) != 1 || orders[0].ID == "" {
		t.Fatalf("alice open orders = %+v", orders)
	}
	if balances := ex.Balances("alice"); len(balances) != 2 {
		t.Fatalf("alice balances = %+v", balances)
	}

	records, err := memory.RecordsAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[0].SchemaVersion != store.CurrentSchemaVersion ||
		records[0].MarketID != "BTC-USDT" || len(records[0].Journal) != 1 ||
		records[0].Command.RequestKey.AccountID != "alice" ||
		records[1].Command.RequestKey.AccountID != "bob" {
		t.Fatalf("persisted scoped records = %+v", records)
	}
}

func TestDifferentAssetScalesReleaseRoundingDust(t *testing.T) {
	t.Parallel()

	market := domain.Market{
		ID:                 "BTC-USDT",
		BaseAsset:          "BTC",
		QuoteAsset:         "USDT",
		BaseScale:          100,
		QuoteScale:         100,
		PriceTick:          1,
		QuantityStep:       1,
		MinQuantity:        1,
		MinNotional:        1,
		MakerFeeBPS:        0,
		TakerFeeBPS:        0,
		ConfigurationEpoch: 1,
	}
	memory := store.NewMemory()
	ex, err := exchange.New(market, memory, memory)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, ex, "buyer-fund", "buyer", "USDT", 10)
	fund(t, ex, "seller-fund", "seller", "BTC", 3)
	buy := submit(t, ex, domain.NewOrder{
		ClientOrderID: "rounded-buy",
		AccountID:     "buyer",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         333,
		Quantity:      3,
	})
	assertBalance(t, ex, "buyer", "USDT", 0, 10)

	submit(t, ex, domain.NewOrder{
		ClientOrderID: "rounded-sell",
		AccountID:     "seller",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         333,
		Quantity:      3,
	})
	assertBalance(t, ex, "buyer", "USDT", 1, 0)
	assertBalance(t, ex, "buyer", "BTC", 3, 0)
	assertBalance(t, ex, "seller", "USDT", 9, 0)
	order, found := ex.Order(buy.OrderID)
	if !found || order.Status != domain.OrderStatusFilled || order.HeldAmount != 0 || order.HeldAsset != "" {
		t.Fatalf("rounded maker order = %+v, found=%t", order, found)
	}
	trades := ex.Trades("buyer")
	if len(trades) != 1 || trades[0].QuoteAmount != 9 {
		t.Fatalf("rounded trades = %+v", trades)
	}
	if err := ex.Validate(); err != nil {
		t.Fatal(err)
	}
	assertJournalBalanced(t, ex)
}

func FuzzExchange(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte{9, 8, 7, 6, 5, 4, 3, 2, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		ex, _ := mustExchange(t)
		for _, account := range []domain.AccountID{"a", "b", "c"} {
			fund(t, ex, "fund-"+string(account)+"-base", account, "BTC", 1_000_000)
			fund(t, ex, "fund-"+string(account)+"-quote", account, "USDT", 100_000_000)
		}
		limit := len(data)
		if limit > 40 {
			limit = 40
		}
		for i := 0; i < limit; i++ {
			value := data[i]
			account := domain.AccountID(string(rune('a' + value%3)))
			side := domain.SideBuy
			if value&1 == 1 {
				side = domain.SideSell
			}
			orderType := domain.OrderTypeLimit
			tif := domain.TimeInForceGTC
			price := int64(value%100 + 1)
			quantity := int64(value%50 + 1)
			quoteBudget := int64(0)
			if value%5 == 0 {
				orderType = domain.OrderTypeMarket
				tif = domain.TimeInForceIOC
				price = 0
				if side == domain.SideBuy {
					quoteBudget = int64(value%100 + 1)
					quantity = 0
				}
			} else {
				switch value % 3 {
				case 1:
					tif = domain.TimeInForceIOC
				case 2:
					tif = domain.TimeInForceFOK
				}
			}
			_, err := ex.Submit(context.Background(), domain.NewOrder{
				ClientOrderID: fmt.Sprintf("fuzz-%d-%d", i, value),
				AccountID:     account,
				Side:          side,
				Type:          orderType,
				TimeInForce:   tif,
				Price:         price,
				Quantity:      quantity,
				QuoteBudget:   quoteBudget,
			})
			if err != nil {
				t.Fatalf("submit byte %d at %d: %v", value, i, err)
			}
			if err := ex.Validate(); err != nil {
				t.Fatalf("validate after byte %d at %d: %v", value, i, err)
			}
		}
		assertJournalBalanced(t, ex)
	})
}

func mustExchange(t testing.TB) (*exchange.Exchange, *store.Memory) {
	t.Helper()
	memory := store.NewMemory()
	ex, err := exchange.New(testMarket(), memory, memory)
	if err != nil {
		t.Fatal(err)
	}
	return ex, memory
}

func testMarket() domain.Market {
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

func fund(t testing.TB, ex *exchange.Exchange, requestID string, accountID domain.AccountID, asset domain.Asset, amount int64) {
	t.Helper()
	if _, err := ex.Fund(context.Background(), domain.FundRequest{
		RequestID: requestID,
		AccountID: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func submit(t testing.TB, ex *exchange.Exchange, request domain.NewOrder) domain.Result {
	t.Helper()
	result, err := ex.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func tradesFrom(result domain.Result) []domain.Trade {
	var trades []domain.Trade
	for _, event := range result.Events {
		if event.Type == domain.EventTradeExecuted && event.Trade != nil {
			trades = append(trades, *event.Trade)
		}
	}
	return trades
}

func hasEvent(result domain.Result, eventType domain.EventType) bool {
	for _, event := range result.Events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func assertBalance(t testing.TB, ex *exchange.Exchange, accountID domain.AccountID, asset domain.Asset, available, held int64) {
	t.Helper()
	got := ex.Balance(accountID, asset)
	if got.Available != available || got.Held != held {
		t.Fatalf("%s %s balance = available %d held %d, want %d/%d",
			accountID, asset, got.Available, got.Held, available, held)
	}
}

func assertJournalBalanced(t testing.TB, ex *exchange.Exchange) {
	t.Helper()
	for _, tx := range ex.Journal() {
		sums := make(map[domain.Asset]int64)
		for _, entry := range tx.Entries {
			sums[entry.Asset] += entry.Amount
		}
		for asset, sum := range sums {
			if sum != 0 {
				t.Fatalf("transaction %s asset %s sums to %d", tx.ID, asset, sum)
			}
		}
	}
}
