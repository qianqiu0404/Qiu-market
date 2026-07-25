package orderbook_test

import (
	"fmt"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/orderbook"
)

func TestPriceTimePriorityAndPartialFill(t *testing.T) {
	t.Parallel()

	book := mustBook(t)
	first := restingOrder("s1", "seller-1", domain.SideSell, 100, 50, 1)
	second := restingOrder("s2", "seller-2", domain.SideSell, 100, 50, 2)
	third := restingOrder("s3", "seller-3", domain.SideSell, 101, 50, 3)
	for _, order := range []domain.Order{first, second, third} {
		if err := book.Add(order); err != nil {
			t.Fatal(err)
		}
	}

	incoming := domain.Order{
		ID:                "b1",
		AccountID:         "buyer",
		MarketID:          "BTC-USDT",
		Side:              domain.SideBuy,
		Type:              domain.OrderTypeLimit,
		TimeInForce:       domain.TimeInForceIOC,
		Price:             101,
		OriginalQuantity:  75,
		RemainingQuantity: 75,
		Status:            domain.OrderStatusReceived,
		AcceptedSequence:  4,
		LastSequence:      4,
	}
	result, err := book.Match(&incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fills) != 2 {
		t.Fatalf("fills = %d, want 2", len(result.Fills))
	}
	if result.Fills[0].MakerOrderID != "s1" || result.Fills[0].Quantity != 50 {
		t.Fatalf("first fill = %+v", result.Fills[0])
	}
	if result.Fills[1].MakerOrderID != "s2" || result.Fills[1].Quantity != 25 {
		t.Fatalf("second fill = %+v", result.Fills[1])
	}
	if incoming.RemainingQuantity != 0 || incoming.FilledQuantity != 75 {
		t.Fatalf("incoming = %+v", incoming)
	}
	snapshot := book.Snapshot()
	if len(snapshot.Asks) != 2 || snapshot.Asks[0].ID != "s2" || snapshot.Asks[0].RemainingQuantity != 25 ||
		snapshot.Asks[1].ID != "s3" {
		t.Fatalf("asks after match = %+v", snapshot.Asks)
	}
	if err := book.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFOKExcludesSelfLiquidity(t *testing.T) {
	t.Parallel()

	book := mustBook(t)
	if err := book.Add(restingOrder("self", "alice", domain.SideSell, 100, 100, 1)); err != nil {
		t.Fatal(err)
	}
	if err := book.Add(restingOrder("other", "bob", domain.SideSell, 100, 100, 2)); err != nil {
		t.Fatal(err)
	}
	fillable, err := book.CanFillFOK("alice", domain.SideBuy, 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if fillable {
		t.Fatal("FOK must not skip over self liquidity under cancel-taker policy")
	}
}

func TestMarketBuyUsesQuoteBudget(t *testing.T) {
	t.Parallel()

	book := mustBook(t)
	if err := book.Add(restingOrder("ask", "seller", domain.SideSell, 100, 20, 1)); err != nil {
		t.Fatal(err)
	}
	incoming := domain.Order{
		ID:                   "market-buy",
		AccountID:            "buyer",
		MarketID:             "BTC-USDT",
		Side:                 domain.SideBuy,
		Type:                 domain.OrderTypeMarket,
		TimeInForce:          domain.TimeInForceIOC,
		OriginalQuoteBudget:  550,
		RemainingQuoteBudget: 550,
		Status:               domain.OrderStatusReceived,
		AcceptedSequence:     2,
		LastSequence:         2,
	}
	result, err := book.Match(&incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fills) != 1 || result.Fills[0].Quantity != 5 || result.Fills[0].QuoteAmount != 500 {
		t.Fatalf("market fills = %+v", result.Fills)
	}
	if result.StopReason != orderbook.StopBudgetExhausted || incoming.RemainingQuoteBudget != 50 {
		t.Fatalf("stop=%s remaining_budget=%d", result.StopReason, incoming.RemainingQuoteBudget)
	}
}

func TestCancelPreservesFIFO(t *testing.T) {
	t.Parallel()

	book := mustBook(t)
	for i := 1; i <= 3; i++ {
		order := restingOrder(domain.OrderID(fmt.Sprintf("s%d", i)), domain.AccountID(fmt.Sprintf("seller-%d", i)), domain.SideSell, 100, 10, uint64(i))
		if err := book.Add(order); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := book.Cancel("s2"); err != nil {
		t.Fatal(err)
	}
	snapshot := book.Snapshot()
	if len(snapshot.Asks) != 2 || snapshot.Asks[0].ID != "s1" || snapshot.Asks[1].ID != "s3" {
		t.Fatalf("asks after cancel = %+v", snapshot.Asks)
	}
}

func BenchmarkMatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		book, _ := orderbook.New(1)
		for level := 0; level < 20; level++ {
			_ = book.Add(restingOrder(
				domain.OrderID(fmt.Sprintf("ask-%d", level)),
				domain.AccountID(fmt.Sprintf("seller-%d", level)),
				domain.SideSell,
				int64(100+level),
				100,
				uint64(level+1),
			))
		}
		incoming := domain.Order{
			ID:                "buyer",
			AccountID:         "buyer",
			MarketID:          "BTC-USDT",
			Side:              domain.SideBuy,
			Type:              domain.OrderTypeLimit,
			TimeInForce:       domain.TimeInForceIOC,
			Price:             119,
			OriginalQuantity:  2_000,
			RemainingQuantity: 2_000,
			Status:            domain.OrderStatusReceived,
			AcceptedSequence:  21,
			LastSequence:      21,
		}
		_, _ = book.Match(&incoming)
	}
}

func mustBook(t *testing.T) *orderbook.Book {
	t.Helper()
	book, err := orderbook.New(1)
	if err != nil {
		t.Fatal(err)
	}
	return book
}

func restingOrder(id domain.OrderID, accountID domain.AccountID, side domain.Side, price, quantity int64, sequence uint64) domain.Order {
	return domain.Order{
		ID:                id,
		ClientOrderID:     string(id),
		AccountID:         accountID,
		MarketID:          "BTC-USDT",
		Side:              side,
		Type:              domain.OrderTypeLimit,
		TimeInForce:       domain.TimeInForceGTC,
		Price:             price,
		OriginalQuantity:  quantity,
		RemainingQuantity: quantity,
		Status:            domain.OrderStatusOpen,
		AcceptedSequence:  sequence,
		LastSequence:      sequence,
	}
}
