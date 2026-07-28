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
	corestore "github.com/the-web3/s78-market-services/trading/store"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func TestPostgresEventSnapshotOutboxAndRecovery(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := postgresstore.EnsureSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}

	market := domain.DefaultBTCUSDTMarket()
	market.ID = domain.MarketID(fmt.Sprintf("BTC-USDT-TEST-%d", time.Now().UnixNano()))
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_projection_checkpoint WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_outbox_checkpoint WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_ledger_entry WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_balance WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_trade WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_order WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_event_feed WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_outbox WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_snapshot WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_event_batch WHERE market_id=$1`, market.ID)
		_, _ = pool.Exec(cleanupContext, `DELETE FROM trading_market WHERE market_id=$1`, market.ID)
	}()

	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		t.Fatal(err)
	}
	trading, err := exchange.New(market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	mustFund(t, trading, "fund-maker", "maker", "BTC", 20_000_000)
	mustFund(t, trading, "fund-taker", "taker", "USDT", 20_000_000_000)
	maker := mustSubmit(t, trading, domain.NewOrder{
		ClientOrderID: "maker-sell",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000_000_000,
		Quantity:      10_000_000,
	})
	takerRequest := domain.NewOrder{
		ClientOrderID: "taker-buy",
		AccountID:     "taker",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         60_200_000_000,
		Quantity:      7_000_000,
	}
	taker := mustSubmit(t, trading, takerRequest)
	if taker.Status != domain.OrderStatusFilled {
		t.Fatalf("taker status = %s", taker.Status)
	}
	snapshot, err := trading.SaveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence != 4 {
		t.Fatalf("snapshot sequence = %d, want 4", snapshot.Sequence)
	}
	if _, err := trading.Cancel(ctx, domain.CancelOrder{
		RequestID: "cancel-maker",
		AccountID: "maker",
		OrderID:   maker.OrderID,
	}); err != nil {
		t.Fatal(err)
	}
	beforeHash, err := trading.StateHash()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := exchange.Restore(ctx, market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	afterHash, err := restored.StateHash()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Sequence() != 5 || afterHash != beforeHash {
		t.Fatalf("restored sequence/hash = %d/%s, want 5/%s", restored.Sequence(), afterHash, beforeHash)
	}
	assertProjections(t, ctx, pool, persistence, restored, maker.OrderID, taker.OrderID)

	for _, table := range []string{
		"trading_projection_checkpoint",
		"trading_ledger_entry",
		"trading_balance",
		"trading_trade",
		"trading_order",
	} {
		if _, err := pool.Exec(ctx, `DELETE FROM `+table+` WHERE market_id=$1`, market.ID); err != nil {
			t.Fatalf("damage %s projection: %v", table, err)
		}
	}
	if err := persistence.RebuildProjections(ctx); err != nil {
		t.Fatal(err)
	}
	assertProjections(t, ctx, pool, persistence, restored, maker.OrderID, taker.OrderID)

	recordCountBeforeRetry := len(mustRecords(t, persistence))
	retry, err := restored.Submit(ctx, takerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Sequence != taker.Sequence || len(mustRecords(t, persistence)) != recordCountBeforeRetry {
		t.Fatalf("retry sequence/records = %d/%d", retry.Sequence, len(mustRecords(t, persistence)))
	}

	outbox, err := persistence.OutboxAfter(ctx, postgresstore.Cursor{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 8 {
		t.Fatalf("outbox events = %d, want 8", len(outbox))
	}
	publish, err := persistence.PublishOutboxBatch(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if publish.Published != len(outbox) ||
		publish.Checkpoint.Sequence != outbox[len(outbox)-1].Sequence ||
		publish.Checkpoint.EventIndex != outbox[len(outbox)-1].EventIndex {
		t.Fatalf("outbox publish = %+v, events=%d", publish, len(outbox))
	}
	firstCursor := postgresstore.Cursor{Sequence: outbox[2].Sequence, EventIndex: outbox[2].EventIndex}
	remaining, err := persistence.FeedAfter(ctx, firstCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != len(outbox)-3 ||
		remaining[0].Sequence < firstCursor.Sequence ||
		(remaining[0].Sequence == firstCursor.Sequence && remaining[0].EventIndex <= firstCursor.EventIndex) {
		t.Fatalf("cursor replay = %+v after %+v", remaining, firstCursor)
	}
	published, err := persistence.OutboxAfter(ctx, postgresstore.Cursor{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || published[0].PublishedAt == nil {
		t.Fatalf("published outbox event = %+v", published)
	}
	deleted, err := persistence.CleanupPublishedOutbox(
		ctx,
		time.Now().Add(time.Hour),
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != int64(len(outbox)) {
		t.Fatalf("cleaned published outbox = %d, want %d", deleted, len(outbox))
	}

	stale, err := exchange.New(market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = stale.Fund(ctx, domain.FundRequest{
		RequestID: "stale-writer",
		AccountID: "other",
		Asset:     "USDT",
		Amount:    1,
	})
	if !errors.Is(err, exchange.ErrPersistence) || !errors.Is(err, corestore.ErrSequenceConflict) {
		t.Fatalf("stale writer error = %v", err)
	}
	sequence, err := persistence.CurrentSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 5 {
		t.Fatalf("stream sequence after stale writer = %d, want 5", sequence)
	}

	conflict := market
	conflict.MakerFeeBPS++
	if _, err := postgresstore.New(ctx, pool, conflict); !errors.Is(err, postgresstore.ErrMarketConfigConflict) {
		t.Fatalf("market config conflict error = %v", err)
	}
}

func assertProjections(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	persistence *postgresstore.Store,
	trading *exchange.Exchange,
	makerOrderID domain.OrderID,
	takerOrderID domain.OrderID,
) {
	t.Helper()

	makerOrder, exists, err := persistence.GetOrder(ctx, makerOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || makerOrder.Status != domain.OrderStatusCanceled {
		t.Fatalf("maker order projection = %+v exists=%v", makerOrder, exists)
	}
	takerOrder, exists, err := persistence.GetOrder(ctx, takerOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || takerOrder.Status != domain.OrderStatusFilled {
		t.Fatalf("taker order projection = %+v exists=%v", takerOrder, exists)
	}
	orders, err := persistence.ListOrders(ctx, "maker", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].ID != makerOrderID {
		t.Fatalf("maker order projections = %+v", orders)
	}
	openOrders, err := persistence.ListOrders(ctx, "maker", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(openOrders) != 0 {
		t.Fatalf("maker open projections = %+v", openOrders)
	}
	trades, err := persistence.ListTrades(ctx, "taker", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].TakerOrderID != takerOrderID {
		t.Fatalf("taker trade projections = %+v", trades)
	}
	balances, err := persistence.Balances(ctx, "maker")
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 2 {
		t.Fatalf("maker balance projections = %+v", balances)
	}
	for _, balance := range balances {
		actual := trading.Balance("maker", balance.Asset)
		if balance.Available != actual.Available || balance.Held != actual.Held {
			t.Fatalf("balance projection %s = %+v, runtime=%+v", balance.Asset, balance, actual)
		}
	}
	checkpoint, exists, err := persistence.ProjectionCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || checkpoint.Sequence != trading.Sequence() || checkpoint.EventIndex != 1 {
		t.Fatalf("projection checkpoint = %+v exists=%v", checkpoint, exists)
	}

	expectedLedgerEntries := 0
	for _, record := range mustRecords(t, persistence) {
		for _, transaction := range record.Journal {
			expectedLedgerEntries += len(transaction.Entries)
		}
	}
	var ledgerEntries int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM trading_ledger_entry WHERE market_id=$1`,
		trading.Market().ID,
	).Scan(&ledgerEntries); err != nil {
		t.Fatal(err)
	}
	if ledgerEntries != expectedLedgerEntries {
		t.Fatalf("ledger projection entries = %d, want %d", ledgerEntries, expectedLedgerEntries)
	}
}

func mustFund(
	t *testing.T,
	trading *exchange.Exchange,
	requestID string,
	accountID domain.AccountID,
	asset domain.Asset,
	amount int64,
) {
	t.Helper()
	if _, err := trading.Fund(context.Background(), domain.FundRequest{
		RequestID: requestID,
		AccountID: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func mustSubmit(t *testing.T, trading *exchange.Exchange, request domain.NewOrder) domain.Result {
	t.Helper()
	result, err := trading.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustRecords(t *testing.T, persistence *postgresstore.Store) []corestore.Record {
	t.Helper()
	records, err := persistence.RecordsAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
