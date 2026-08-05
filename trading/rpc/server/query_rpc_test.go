package server

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/query"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

func TestQueryRPCsMapPagesAndRoundTripOpaqueCursors(t *testing.T) {
	now := time.Date(2026, 8, 5, 2, 3, 4, 0, time.UTC)
	reader := newQueryRPCReader(now)
	server := newQueryRPCServer(t, reader, now)
	ctx := context.Background()

	orders, err := server.ListOrders(ctx, &tradingv1.ListOrdersRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Scope: "all", Limit: 25,
	})
	if err != nil || len(orders.Orders) != 1 || orders.NextCursor == "" {
		t.Fatalf("orders=%+v err=%v", orders, err)
	}
	if orders.Orders[0].AverageFillPrice != "60000" ||
		orders.Orders[0].CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("order mapping = %+v", orders.Orders[0])
	}
	if _, err := server.ListOrders(ctx, &tradingv1.ListOrdersRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Scope: "all",
		Cursor: orders.NextCursor,
	}); err != nil {
		t.Fatalf("orders cursor replay: %v", err)
	}
	if reader.orderCursor == nil || reader.orderCursor.AcceptedSequence != 10 ||
		reader.orderCursor.OrderID != "order-1" {
		t.Fatalf("decoded order cursor = %+v", reader.orderCursor)
	}

	trades, err := server.ListAccountTrades(ctx, &tradingv1.ListAccountTradesRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Limit: 25,
	})
	if err != nil || len(trades.Trades) != 1 || trades.NextCursor == "" {
		t.Fatalf("trades=%+v err=%v", trades, err)
	}
	if trades.Trades[0].OrderId != "order-1" || trades.Trades[0].FeeAmount != "0.000001" {
		t.Fatalf("account trade mapping = %+v", trades.Trades[0])
	}
	if _, err := server.ListAccountTrades(ctx, &tradingv1.ListAccountTradesRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Cursor: trades.NextCursor,
	}); err != nil {
		t.Fatalf("trade cursor replay: %v", err)
	}
	if reader.tradeCursor == nil || reader.tradeCursor.TradeID != "trade-1" {
		t.Fatalf("decoded trade cursor = %+v", reader.tradeCursor)
	}

	events, err := server.ListOrderEvents(ctx, &tradingv1.ListOrderEventsRequest{
		MarketId: "BTC-USDT", AccountId: "alice", OrderId: "order-1", Limit: 25,
	})
	if err != nil || len(events.Events) != 1 || events.NextCursor == "" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if events.Events[0].Fee == nil || events.Events[0].Fee.Amount != "0.000001" ||
		len(events.Events[0].BalanceEffects) != 1 ||
		events.Events[0].BalanceEffects[0].Amount != "-60" {
		t.Fatalf("order event mapping = %+v", events.Events[0])
	}
	if _, err := server.ListOrderEvents(ctx, &tradingv1.ListOrderEventsRequest{
		MarketId: "BTC-USDT", AccountId: "alice", OrderId: "order-1",
		Cursor: events.NextCursor,
	}); err != nil {
		t.Fatalf("timeline cursor replay: %v", err)
	}
	if reader.timelineCursor == nil || reader.timelineCursor.TimelineIndex != 1 {
		t.Fatalf("decoded timeline cursor = %+v", reader.timelineCursor)
	}

	entries, err := server.ListLedgerEntries(ctx, &tradingv1.ListLedgerEntriesRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Asset: "USDT",
		Reason: "trade_settlement", Limit: 25,
	})
	if err != nil || len(entries.Entries) != 1 || entries.NextCursor == "" {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	if entries.Entries[0].Amount != "-60" || entries.Entries[0].Bucket != "held" {
		t.Fatalf("ledger mapping = %+v", entries.Entries[0])
	}
	if _, err := server.ListLedgerEntries(ctx, &tradingv1.ListLedgerEntriesRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Asset: "USDT",
		Reason: "trade_settlement", Cursor: entries.NextCursor,
	}); err != nil {
		t.Fatalf("ledger cursor replay: %v", err)
	}
	if reader.ledgerCursor == nil || reader.ledgerCursor.TransactionID != "tx-1" {
		t.Fatalf("decoded ledger cursor = %+v", reader.ledgerCursor)
	}
}

func TestQueryRPCValidationAndReaderAbsenceFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 5, 2, 3, 4, 0, time.UTC)
	server := newQueryRPCServer(t, newQueryRPCReader(now), now)
	ctx := context.Background()

	open := true
	for name, request := range map[string]*tradingv1.ListOrdersRequest{
		"scope-conflict": {
			MarketId: "BTC-USDT", AccountId: "alice", Scope: "open", OpenOnly: &open,
		},
		"p1-filter": {
			MarketId: "BTC-USDT", AccountId: "alice", Status: "filled",
		},
		"large-limit": {
			MarketId: "BTC-USDT", AccountId: "alice", Limit: 101,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := server.ListOrders(ctx, request)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("error = %v", err)
			}
		})
	}

	first, err := server.ListOrders(ctx, &tradingv1.ListOrdersRequest{
		MarketId: "BTC-USDT", AccountId: "alice", Scope: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.ListOrders(ctx, &tradingv1.ListOrdersRequest{
		MarketId: "BTC-USDT", AccountId: "mallory", Scope: "all",
		Cursor: first.NextCursor,
	})
	if status.Code(err) != codes.InvalidArgument ||
		!containsStatusMessage(err, "invalid_cursor") {
		t.Fatalf("wrong-account cursor error = %v", err)
	}

	withoutReader := &Server{engine: queryRPCMarketEngine{}}
	_, err = withoutReader.ListAccountTrades(ctx, &tradingv1.ListAccountTradesRequest{
		MarketId: "BTC-USDT", AccountId: "alice",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("missing reader error = %v", err)
	}
	if _, err := server.ListTrades(ctx, &tradingv1.ListTradesRequest{
		MarketId: "BTC-USDT", AccountId: "alice",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("public ListTrades accepted account_id: %v", err)
	}

	_, err = New(queryRPCMarketEngine{}, nil, Config{
		EventBatchSize: 10, EventPollEvery: time.Millisecond, Queries: newQueryRPCReader(now),
	})
	if err == nil {
		t.Fatal("server accepted a query reader without persistent cursor signing keys")
	}
}

func TestFormatSignedDecimalHandlesInt64Minimum(t *testing.T) {
	formatted, err := formatSignedDecimal(-9_223_372_036_854_775_808, 1_000_000)
	if err != nil || formatted != "-9223372036854.775808" {
		t.Fatalf("formatted=%q err=%v", formatted, err)
	}
}

type queryRPCMarketEngine struct{ Engine }

func (queryRPCMarketEngine) Market() (domain.Market, error) {
	return domain.DefaultBTCUSDTMarket(), nil
}

type queryRPCReader struct {
	now            time.Time
	orderCursor    *query.OrderCursor
	tradeCursor    *query.TradeCursor
	timelineCursor *query.TimelineCursor
	ledgerCursor   *query.LedgerCursor
}

func newQueryRPCReader(now time.Time) *queryRPCReader {
	return &queryRPCReader{now: now}
}

func (r *queryRPCReader) order() query.OrderView {
	average := int64(60_000_000_000)
	return query.OrderView{
		Order: domain.Order{
			ID: "order-1", ClientOrderID: "client-1", AccountID: "alice",
			MarketID: "BTC-USDT", Side: domain.SideBuy, Type: domain.OrderTypeLimit,
			TimeInForce: domain.TimeInForceGTC, Price: 60_000_000_000,
			OriginalQuantity: 200_000, RemainingQuantity: 100_000,
			FilledQuantity: 100_000, SpentQuote: 60_000_000,
			HeldAsset: "USDT", HeldAmount: 60_000_000,
			Status: domain.OrderStatusPartiallyFilled, AcceptedSequence: 10,
			LastSequence: 11,
		},
		AverageFillPrice: &average, CreatedAt: r.now, UpdatedAt: r.now,
	}
}

func (r *queryRPCReader) GetOrder(
	_ context.Context,
	accountID domain.AccountID,
	orderID domain.OrderID,
) (query.OrderView, bool, error) {
	order := r.order()
	if accountID != order.Order.AccountID || orderID != order.Order.ID {
		return query.OrderView{}, false, nil
	}
	return order, true, nil
}

func (r *queryRPCReader) ListOrders(
	_ context.Context,
	_ domain.AccountID,
	_ query.OrderFilter,
	cursor *query.OrderCursor,
	_ int,
) (query.OrderPage, error) {
	r.orderCursor = cursor
	return query.OrderPage{
		Orders: []query.OrderView{r.order()},
		NextCursor: &query.OrderCursor{
			AcceptedSequence: 10, OrderID: "order-1",
		},
	}, nil
}

func (r *queryRPCReader) ListAccountTrades(
	_ context.Context,
	_ domain.AccountID,
	_ query.TradeFilter,
	cursor *query.TradeCursor,
	_ int,
) (query.TradePage, error) {
	r.tradeCursor = cursor
	return query.TradePage{
		Trades: []query.AccountTrade{{
			ID: "trade-1", MarketID: "BTC-USDT", OrderID: "order-1",
			Side: domain.SideBuy, LiquidityRole: domain.LiquidityRoleTaker,
			Price: 60_000_000_000, Quantity: 100_000, QuoteAmount: 60_000_000,
			FeeAsset: "BTC", FeeAmount: 100, FeeRateBPS: 20,
			Sequence: 11, EventIndex: 2, OccurredAt: r.now,
		}},
		NextCursor: &query.TradeCursor{Sequence: 11, EventIndex: 2, TradeID: "trade-1"},
	}, nil
}

func (r *queryRPCReader) ListOrderEvents(
	_ context.Context,
	_ domain.AccountID,
	_ domain.OrderID,
	cursor *query.TimelineCursor,
	_ int,
) (query.OrderEventPage, error) {
	r.timelineCursor = cursor
	quantity := int64(100_000)
	price := int64(60_000_000_000)
	remaining := int64(100_000)
	return query.OrderEventPage{
		Events: []query.OrderEvent{{
			EventID: "11:2:1", MarketID: "BTC-USDT", OrderID: "order-1",
			Sequence: 11, EventIndex: 2, TimelineIndex: 1,
			SourceKind: query.SourceKindEvent, Type: domain.EventTradeExecuted,
			Status: domain.OrderStatusPartiallyFilled, Quantity: &quantity, Price: &price,
			RemainingQuantity: &remaining, TradeID: "trade-1",
			Fee: &query.FeeView{
				Asset: "BTC", Amount: 100, RateBPS: 20, Role: domain.LiquidityRoleTaker,
			},
			BalanceEffects: []query.BalanceEffect{{
				Asset: "USDT", Bucket: query.BalanceBucketHeld, Amount: -60_000_000,
				Reason: query.LedgerReasonTradeSettlement, TransactionID: "tx-1",
			}},
			OccurredAt: r.now,
		}},
		NextCursor: &query.TimelineCursor{Sequence: 11, EventIndex: 2, TimelineIndex: 1},
	}, nil
}

func (r *queryRPCReader) ListLedgerEntries(
	_ context.Context,
	_ domain.AccountID,
	_ query.LedgerFilter,
	cursor *query.LedgerCursor,
	_ int,
) (query.LedgerPage, error) {
	r.ledgerCursor = cursor
	return query.LedgerPage{
		Entries: []query.LedgerEntry{{
			EntryID: "11:tx-1:2", MarketID: "BTC-USDT", Sequence: 11,
			TransactionID: "tx-1", EntryIndex: 2, Asset: "USDT",
			Bucket: query.BalanceBucketHeld, Amount: -60_000_000,
			Reason:    query.LedgerReasonTradeSettlement,
			Reference: "matched-trade:trade-1", OrderID: "order-1", TradeID: "trade-1",
			OccurredAt: r.now,
		}},
		NextCursor: &query.LedgerCursor{Sequence: 11, TransactionID: "tx-1", EntryIndex: 2},
	}, nil
}

func newQueryRPCServer(
	t *testing.T,
	reader query.Reader,
	now time.Time,
) *Server {
	t.Helper()
	codec := mustCursorCodec(t, CursorConfig{
		Current: cursorTestKey("rpc-test", 0x44), Now: func() time.Time { return now },
	})
	return &Server{engine: queryRPCMarketEngine{}, queries: reader, cursors: codec}
}

func containsStatusMessage(err error, value string) bool {
	return err != nil && len(status.Convert(err).Message()) >= len(value) &&
		status.Convert(err).Message()[:len(value)] == value
}

var _ query.Reader = (*queryRPCReader)(nil)
