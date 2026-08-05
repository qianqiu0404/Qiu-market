package postgres

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/query"
	corestore "github.com/the-web3/s78-market-services/trading/store"
)

func TestBuildLifecycleRowsReconstructsBothTradeViewsAndMarketBudget(t *testing.T) {
	market, records, makerID, takerID := lifecycleFixture(t)

	first, err := rebuildLifecycleFixture(records)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rebuildLifecycleFixture(records)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("lifecycle projection is not deterministic")
	}

	var makerTrade, takerTrade *query.OrderEvent
	var makerCanceled *query.OrderEvent
	for index := range first {
		event := &first[index].Event
		if event.Type == domain.EventTradeExecuted && event.OrderID == makerID {
			makerTrade = event
		}
		if event.Type == domain.EventTradeExecuted && event.OrderID == takerID {
			takerTrade = event
		}
		if event.Type == domain.EventOrderCanceled && event.OrderID == makerID &&
			event.Reason == "user_requested" {
			makerCanceled = event
		}
	}
	if makerTrade == nil || takerTrade == nil || makerCanceled == nil {
		t.Fatalf("missing lifecycle rows: maker=%v taker=%v canceled=%v", makerTrade, takerTrade, makerCanceled)
	}
	if takerTrade.TimelineIndex != 0 || makerTrade.TimelineIndex != 1 {
		t.Fatalf(
			"trade timeline indexes taker/maker=%d/%d",
			takerTrade.TimelineIndex,
			makerTrade.TimelineIndex,
		)
	}
	if takerTrade.RemainingQuantity != nil || takerTrade.RemainingQuoteBudget == nil ||
		*takerTrade.RemainingQuoteBudget != 0 {
		t.Fatalf("market buy remaining state = %+v", takerTrade)
	}
	if makerTrade.RemainingQuantity == nil || *makerTrade.RemainingQuantity != 3_000_000 ||
		makerTrade.RemainingQuoteBudget != nil {
		t.Fatalf("maker remaining state = %+v", makerTrade)
	}
	if takerTrade.Fee == nil || takerTrade.Fee.Role != domain.LiquidityRoleTaker ||
		takerTrade.Fee.Asset != market.BaseAsset || takerTrade.Fee.Amount != 14_000 {
		t.Fatalf("taker fee = %+v", takerTrade.Fee)
	}
	if makerTrade.Fee == nil || makerTrade.Fee.Role != domain.LiquidityRoleMaker ||
		makerTrade.Fee.Asset != market.QuoteAsset || makerTrade.Fee.Amount != 4_200_000 {
		t.Fatalf("maker fee = %+v", makerTrade.Fee)
	}
	if len(takerTrade.BalanceEffects) != 2 || len(makerTrade.BalanceEffects) != 2 {
		t.Fatalf(
			"account-scoped settlement effects taker/maker=%+v/%+v",
			takerTrade.BalanceEffects,
			makerTrade.BalanceEffects,
		)
	}
	if len(makerCanceled.BalanceEffects) != 2 {
		t.Fatalf("cancel release effects = %+v", makerCanceled.BalanceEffects)
	}
	for _, effect := range append(
		append([]query.BalanceEffect(nil), takerTrade.BalanceEffects...),
		makerTrade.BalanceEffects...,
	) {
		if effect.TransactionID == "" || effect.Reason != query.LedgerReasonTradeSettlement {
			t.Fatalf("invalid settlement effect = %+v", effect)
		}
	}
}

func TestBuildLifecycleRowsRejectsCorruptJournalReference(t *testing.T) {
	_, records, _, _ := lifecycleFixture(t)
	corrupt := records[2]
	if len(corrupt.Journal) != 1 {
		t.Fatalf("maker submit journal count=%d", len(corrupt.Journal))
	}
	corrupt.Journal[0].Reference = "order-hold:wrong-order"
	_, err := buildLifecycleRows(
		corrupt,
		map[domain.OrderID]domain.Order{},
		time.Unix(3, 0).UTC(),
	)
	if err == nil || !strings.Contains(err.Error(), "reference") {
		t.Fatalf("corrupt journal error=%v", err)
	}
}

func TestValidateLifecycleIntegrityFailsClosedOnCardinalityDamage(t *testing.T) {
	tests := []struct {
		name      string
		integrity lifecycleIntegrity
		want      string
	}{
		{
			name:      "orphaned rows",
			integrity: lifecycleIntegrity{ActualRows: 1},
			want:      "orphaned rows",
		},
		{
			name: "missing row",
			integrity: lifecycleIntegrity{
				Found: true, Sequence: 10, RecordedRows: 4, ActualRows: 3,
			},
			want: "row-count mismatch",
		},
		{
			name: "extra row",
			integrity: lifecycleIntegrity{
				Found: true, Sequence: 10, RecordedRows: 4, ActualRows: 5,
			},
			want: "row-count mismatch",
		},
		{
			name: "row ahead of checkpoint",
			integrity: lifecycleIntegrity{
				Found: true, Sequence: 9, RecordedRows: 4, ActualRows: 4, MaximumSequence: 10,
			},
			want: "ahead of checkpoint",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateLifecycleIntegrity(test.integrity)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("integrity error=%v want %q", err, test.want)
			}
		})
	}

	// Cardinality deliberately does not claim to authenticate payload bytes.
	// A same-count mutation requires a future digest/manifest migration.
	if err := validateLifecycleIntegrity(lifecycleIntegrity{
		Found: true, Sequence: 10, RecordedRows: 4, ActualRows: 4, MaximumSequence: 10,
	}); err != nil {
		t.Fatalf("consistent cardinality rejected: %v", err)
	}
}

func lifecycleFixture(
	t *testing.T,
) (domain.Market, []corestore.Record, domain.OrderID, domain.OrderID) {
	t.Helper()
	ctx := context.Background()
	market := domain.DefaultBTCUSDTMarket()
	memory := corestore.NewMemory()
	venue, err := exchange.New(market, memory, memory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := venue.Fund(ctx, domain.FundRequest{
		RequestID: "fund-maker",
		AccountID: "maker",
		Asset:     market.BaseAsset,
		Amount:    20_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := venue.Fund(ctx, domain.FundRequest{
		RequestID: "fund-taker",
		AccountID: "taker",
		Asset:     market.QuoteAsset,
		Amount:    5_000_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	maker, err := venue.Submit(ctx, domain.NewOrder{
		ClientOrderID: "maker-sell",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000_000_000,
		Quantity:      10_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	taker, err := venue.Submit(ctx, domain.NewOrder{
		ClientOrderID: "taker-market-buy",
		AccountID:     "taker",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		TimeInForce:   domain.TimeInForceIOC,
		QuoteBudget:   4_200_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := venue.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel-maker",
		AccountID: "maker",
		OrderID:   maker.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := memory.RecordsAfter(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	return market, records, maker.OrderID, taker.OrderID
}

func rebuildLifecycleFixture(records []corestore.Record) ([]lifecycleRow, error) {
	previous := make(map[domain.OrderID]domain.Order)
	var all []lifecycleRow
	for _, record := range records {
		rows, err := buildLifecycleRows(
			record,
			previous,
			time.Unix(int64(record.Command.Sequence), 0).UTC(),
		)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		for _, order := range record.Projection.Orders {
			previous[order.ID] = order
		}
	}
	return all, nil
}
