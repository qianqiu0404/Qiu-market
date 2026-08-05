package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/query"
	readmodel "github.com/the-web3/s78-market-services/trading/readmodel/postgres"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func TestReaderAccountScopeKeysetsTimelineLedgerAndRebuild(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := postgresstore.EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	market := domain.DefaultBTCUSDTMarket()
	market.ID = domain.MarketID(fmt.Sprintf("BTC-USDT-READ-%d", time.Now().UnixNano()))
	cleanupReaderMarket(t, pool, market.ID)
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		t.Fatal(err)
	}
	venue, err := exchange.New(market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	mustFundReader(t, venue, "fund-maker", "maker", market.BaseAsset, 30_000_000)
	mustFundReader(t, venue, "fund-taker", "taker", market.QuoteAsset, 10_000_000_000)
	maker := mustSubmitReader(t, venue, domain.NewOrder{
		ClientOrderID: "maker-one",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000_000_000,
		Quantity:      10_000_000,
	})
	mustSubmitReader(t, venue, domain.NewOrder{
		ClientOrderID: "taker-one",
		AccountID:     "taker",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         60_100_000_000,
		Quantity:      7_000_000,
	})
	if _, err := venue.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel-maker",
		AccountID: "maker",
		OrderID:   maker.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	second := mustSubmitReader(t, venue, domain.NewOrder{
		ClientOrderID: "maker-two",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         61_000_000_000,
		Quantity:      5_000_000,
	})

	reader, err := readmodel.New(pool, market)
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := reader.ListOrders(
		ctx,
		"maker",
		query.OrderFilter{Scope: query.OrderScopeAll},
		nil,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Orders) != 1 || firstPage.Orders[0].Order.ID != second.OrderID ||
		firstPage.NextCursor == nil {
		t.Fatalf("first order page = %+v", firstPage)
	}
	// A new head inserted after page one must not duplicate or hide the older
	// accepted-sequence window represented by its cursor.
	third := mustSubmitReader(t, venue, domain.NewOrder{
		ClientOrderID: "maker-three",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         62_000_000_000,
		Quantity:      5_000_000,
	})
	secondPage, err := reader.ListOrders(
		ctx,
		"maker",
		query.OrderFilter{Scope: query.OrderScopeAll},
		firstPage.NextCursor,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Orders) != 1 || secondPage.Orders[0].Order.ID != maker.OrderID {
		t.Fatalf("second order page = %+v", secondPage)
	}
	refreshed, err := reader.ListOrders(
		ctx,
		"maker",
		query.OrderFilter{Scope: query.OrderScopeAll},
		nil,
		1,
	)
	if err != nil || len(refreshed.Orders) != 1 || refreshed.Orders[0].Order.ID != third.OrderID {
		t.Fatalf("refreshed order head = %+v err=%v", refreshed, err)
	}

	makerView, found, err := reader.GetOrder(ctx, "maker", maker.OrderID)
	if err != nil || !found {
		t.Fatalf("maker order found=%v err=%v", found, err)
	}
	if makerView.AverageFillPrice == nil || *makerView.AverageFillPrice != 60_000_000_000 {
		t.Fatalf("average fill price = %+v", makerView.AverageFillPrice)
	}
	if _, found, err := reader.GetOrder(ctx, "taker", maker.OrderID); err != nil || found {
		t.Fatalf("cross-account order found=%v err=%v", found, err)
	}

	trades, err := reader.ListAccountTrades(ctx, "maker", query.TradeFilter{}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades.Trades) != 1 || trades.Trades[0].OrderID != maker.OrderID ||
		trades.Trades[0].LiquidityRole != domain.LiquidityRoleMaker ||
		trades.Trades[0].Side != domain.SideSell || trades.Trades[0].FeeAsset != market.QuoteAsset {
		t.Fatalf("maker account trades = %+v", trades.Trades)
	}

	timeline, err := reader.ListOrderEvents(ctx, "maker", maker.OrderID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var hasMakerTrade, hasCancelRelease bool
	for _, event := range timeline.Events {
		if event.Type == domain.EventTradeExecuted {
			hasMakerTrade = event.TimelineIndex == 1 && event.Fee != nil &&
				event.Fee.Role == domain.LiquidityRoleMaker
		}
		if event.Type == domain.EventOrderCanceled && event.Reason == "user_requested" {
			hasCancelRelease = len(event.BalanceEffects) == 2
		}
	}
	if !hasMakerTrade || !hasCancelRelease {
		t.Fatalf("maker lifecycle = %+v", timeline.Events)
	}

	ledgerPage, err := reader.ListLedgerEntries(
		ctx,
		"maker",
		query.LedgerFilter{Reason: query.LedgerReasonTradeSettlement},
		nil,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgerPage.Entries) != 2 {
		t.Fatalf("maker settlement ledger = %+v", ledgerPage.Entries)
	}
	for _, entry := range ledgerPage.Entries {
		if entry.OrderID != maker.OrderID || entry.TradeID == "" ||
			(entry.Bucket != query.BalanceBucketAvailable && entry.Bucket != query.BalanceBucketHeld) {
			t.Fatalf("private ledger entry = %+v", entry)
		}
	}

	beforeTimeline := timeline.Events
	if err := persistence.RebuildProjections(ctx); err != nil {
		t.Fatal(err)
	}
	afterTimeline, err := reader.ListOrderEvents(ctx, "maker", maker.OrderID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterTimeline.Events) != len(beforeTimeline) {
		t.Fatalf("rebuilt timeline count=%d want=%d", len(afterTimeline.Events), len(beforeTimeline))
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM trading_order_event WHERE market_id=$1`, market.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM trading_order_event_checkpoint WHERE market_id=$1`, market.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := postgresstore.New(ctx, pool, market); err != nil {
		t.Fatalf("automatic event-authority rebuild: %v", err)
	}
	automaticTimeline, err := reader.ListOrderEvents(ctx, "maker", maker.OrderID, nil, 100)
	if err != nil || len(automaticTimeline.Events) != len(beforeTimeline) {
		t.Fatalf(
			"automatic rebuilt timeline count=%d want=%d err=%v",
			len(automaticTimeline.Events),
			len(beforeTimeline),
			err,
		)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE trading_order_event_checkpoint SET sequence=sequence-1 WHERE market_id=$1
	`, market.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListOrderEvents(ctx, "maker", maker.OrderID, nil, 10); !errors.Is(err, readmodel.ErrIntegrity) {
		t.Fatalf("behind lifecycle checkpoint error=%v", err)
	}
}

func cleanupReaderMarket(t *testing.T, pool *pgxpool.Pool, marketID domain.MarketID) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, table := range []string{
			"trading_projection_checkpoint",
			"trading_order_event",
			"trading_order_event_checkpoint",
			"trading_ledger_entry",
			"trading_balance",
			"trading_trade",
			"trading_order",
			"trading_event_feed",
			"trading_outbox_checkpoint",
			"trading_outbox",
			"trading_snapshot",
			"trading_event_batch",
			"trading_market",
		} {
			_, _ = pool.Exec(ctx, `DELETE FROM `+table+` WHERE market_id=$1`, marketID)
		}
		pool.Close()
	})
}

func mustFundReader(
	t *testing.T,
	venue *exchange.Exchange,
	requestID string,
	accountID domain.AccountID,
	asset domain.Asset,
	amount int64,
) {
	t.Helper()
	if _, err := venue.Fund(context.Background(), domain.FundRequest{
		RequestID: requestID,
		AccountID: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func mustSubmitReader(
	t *testing.T,
	venue *exchange.Exchange,
	request domain.NewOrder,
) domain.Result {
	t.Helper()
	result, err := venue.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
