package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/query"
)

func TestAccountTradeViewReturnsOnlyCurrentAccountPerspective(t *testing.T) {
	market := domain.DefaultBTCUSDTMarket()
	trade := domain.Trade{
		ID:              "trade-1",
		MarketID:        market.ID,
		Price:           60_000_000_000,
		Quantity:        1_000_000,
		QuoteAmount:     600_000_000,
		MakerOrderID:    "maker-order",
		TakerOrderID:    "taker-order",
		MakerAccountID:  "maker",
		TakerAccountID:  "taker",
		BuyerAccountID:  "taker",
		SellerAccountID: "maker",
		BuyerFee: domain.Fee{
			AccountID: "taker",
			Asset:     market.BaseAsset,
			Amount:    2_000,
			RateBPS:   20,
			Role:      domain.LiquidityRoleTaker,
		},
		SellerFee: domain.Fee{
			AccountID: "maker",
			Asset:     market.QuoteAsset,
			Amount:    600_000,
			RateBPS:   10,
			Role:      domain.LiquidityRoleMaker,
		},
	}
	view, err := accountTradeView(
		trade,
		"maker",
		9,
		3,
		time.Unix(9, 0).UTC(),
		market.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if view.OrderID != trade.MakerOrderID || view.Side != domain.SideSell ||
		view.LiquidityRole != domain.LiquidityRoleMaker ||
		view.FeeAsset != market.QuoteAsset || view.FeeAmount != trade.SellerFee.Amount {
		t.Fatalf("maker perspective = %+v", view)
	}
	if _, err := accountTradeView(
		trade,
		"other",
		9,
		3,
		time.Unix(9, 0).UTC(),
		market.ID,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("non-participant error=%v", err)
	}
}

func TestOrderViewUsesCheckedFloorAverageAndRejectsTransientState(t *testing.T) {
	market := domain.DefaultBTCUSDTMarket()
	reader := &Reader{market: market}
	createdAt := time.Unix(1, 0).UTC()
	order := domain.Order{
		ID:                "order-1",
		AccountID:         "alice",
		MarketID:          market.ID,
		Status:            domain.OrderStatusPartiallyFilled,
		AcceptedSequence:  1,
		LastSequence:      2,
		FilledQuantity:    3,
		SpentQuote:        10,
		OriginalQuantity:  6,
		RemainingQuantity: 3,
	}
	payload, err := json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	view, err := reader.orderView(payload, 1, 2, createdAt, createdAt.Add(time.Second), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if view.AverageFillPrice == nil || *view.AverageFillPrice != 333_333_333 {
		t.Fatalf("floor average = %+v", view.AverageFillPrice)
	}
	order.Status = domain.OrderStatusReceived
	payload, err = json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.orderView(payload, 1, 2, createdAt, createdAt, "alice"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("transient order status error=%v", err)
	}
}

func TestLedgerEntryRejectsSystemAndMalformedReferences(t *testing.T) {
	market := domain.DefaultBTCUSDTMarket()
	reader := &Reader{market: market}
	now := time.Unix(1, 0).UTC()
	if _, err := reader.ledgerEntry(
		"alice",
		ledger.UserAvailable("alice"),
		ledger.UserHeld("alice"),
		1,
		"fund:0001",
		1,
		ledger.SystemTreasury(market.QuoteAsset),
		market.QuoteAsset,
		-10,
		"virtual-funding:req",
		string(query.LedgerReasonVirtualFund),
		now,
		nil,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("system account error=%v", err)
	}
	if _, err := reader.ledgerEntry(
		"alice",
		ledger.UserAvailable("alice"),
		ledger.UserHeld("alice"),
		1,
		"trade:broken",
		1,
		ledger.UserAvailable("alice"),
		market.BaseAsset,
		10,
		"matched-trade:",
		string(query.LedgerReasonTradeSettlement),
		now,
		nil,
	); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("malformed trade reference error=%v", err)
	}
}
