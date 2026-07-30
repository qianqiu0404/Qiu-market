package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestTradingGRPCDecimalContractAndOwnership(t *testing.T) {
	client, runner := newTestClient(t, nil)
	ctx := context.Background()

	mustFund(t, ctx, client, "fund-maker", "maker", "BTC", "0.2")
	mustFund(t, ctx, client, "fund-taker", "taker", "USDT", "20000")
	makerRequest := &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "maker",
		ClientOrderId: "maker-sell",
		Side:          tradingv1.Side_SIDE_SELL,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:         "60000",
		Quantity:      "0.1",
	}
	maker, err := client.SubmitOrder(ctx, makerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if maker.Sequence != "3" || maker.Status != "open" {
		t.Fatalf("maker result = %+v", maker)
	}
	retry, err := client.SubmitOrder(ctx, makerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Sequence != maker.Sequence || runner.Status().Sequence != 3 {
		t.Fatalf("idempotent retry = %+v status=%+v", retry, runner.Status())
	}

	_, err = client.CancelOrder(ctx, &tradingv1.CancelOrderRequest{
		MarketId:  "BTC-USDT",
		AccountId: "taker",
		RequestId: "forged-cancel",
		OrderId:   maker.OrderId,
	})
	if status.Code(err) != codes.PermissionDenied || runner.Status().Sequence != 3 {
		t.Fatalf("forged cancel error/status = %v/%+v", err, runner.Status())
	}

	taker, err := client.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "taker",
		ClientOrderId: "taker-buy",
		Side:          tradingv1.Side_SIDE_BUY,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_IOC,
		Price:         "60200",
		Quantity:      "0.07",
	})
	if err != nil {
		t.Fatal(err)
	}
	if taker.Sequence != "4" || taker.Status != "filled" {
		t.Fatalf("taker result = %+v", taker)
	}

	balances, err := client.GetBalances(ctx, &tradingv1.GetBalancesRequest{
		MarketId:  "BTC-USDT",
		AccountId: "taker",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBalance(t, balances.Balances, "BTC", "0.06986", "0")
	assertBalance(t, balances.Balances, "USDT", "15800", "0")

	trades, err := client.ListTrades(ctx, &tradingv1.ListTradesRequest{
		MarketId:  "BTC-USDT",
		AccountId: "taker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(trades.Trades) != 1 || trades.Trades[0].Price != "60000" ||
		trades.Trades[0].Quantity != "0.07" {
		t.Fatalf("trades = %+v", trades.Trades)
	}
	makerOrder, err := client.GetOrder(ctx, &tradingv1.GetOrderRequest{
		MarketId:  "BTC-USDT",
		AccountId: "maker",
		OrderId:   maker.OrderId,
	})
	if err != nil {
		t.Fatal(err)
	}
	if makerOrder.Status != "partially_filled" || makerOrder.HeldAmount != "0.03" {
		t.Fatalf("maker order = %+v", makerOrder)
	}
	book, err := client.GetOrderBook(ctx, &tradingv1.GetOrderBookRequest{
		MarketId: "BTC-USDT",
		Levels:   20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if book.Sequence != "4" || len(book.Asks) != 1 ||
		book.Asks[0].Price != "60000" || book.Asks[0].Quantity != "0.03" {
		t.Fatalf("order book = %+v", book)
	}

	_, err = client.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "taker",
		ClientOrderId: "invalid-precision",
		Side:          tradingv1.Side_SIDE_BUY,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:         "60000.0000001",
		Quantity:      "0.01",
	})
	if status.Code(err) != codes.InvalidArgument || runner.Status().Sequence != 4 {
		t.Fatalf("invalid decimal error/status = %v/%+v", err, runner.Status())
	}
}

func TestCancelOrderIdempotencyPrecedesClosedStateCheck(t *testing.T) {
	client, runner := newTestClient(t, nil)
	ctx := context.Background()

	mustFund(t, ctx, client, "cancel-idem-fund", "alice", "USDT", "100")
	order, err := client.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "alice",
		ClientOrderId: "cancel-idem-order",
		Side:          tradingv1.Side_SIDE_BUY,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:         "60000",
		Quantity:      "0.001",
		PostOnly:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &tradingv1.CancelOrderRequest{
		MarketId:  "BTC-USDT",
		AccountId: "alice",
		RequestId: "cancel-idem-request",
		OrderId:   order.OrderId,
	}
	first, err := client.CancelOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.CancelOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != retry.Sequence || first.Status != retry.Status ||
		runner.Status().Sequence != 3 {
		t.Fatalf("cancel retry first=%+v retry=%+v status=%+v", first, retry, runner.Status())
	}

	request.RequestId = "different-cancel-request"
	if _, err := client.CancelOrder(ctx, request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("fresh cancel of closed order error = %v", err)
	}
	if runner.Status().Sequence != 4 {
		t.Fatalf("fresh rejected cancel sequence = %+v", runner.Status())
	}
}

func TestTradingGRPCSubscribeEventsFromCursor(t *testing.T) {
	source := &oneEventSource{event: tradingserver.StoredEvent{
		MarketID:        "BTC-USDT",
		Cursor:          tradingserver.Cursor{Sequence: 1, EventIndex: 1},
		BatchEventCount: 1,
		Event: domain.Event{
			Sequence:  1,
			Index:     1,
			Type:      domain.EventAccountFunded,
			AccountID: "alice",
			Asset:     "USDT",
			Amount:    1_000_000,
		},
	}}
	client, _ := newTestClient(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, err := client.SubscribeEvents(ctx, &tradingv1.SubscribeEventsRequest{
		MarketId:       "BTC-USDT",
		CursorSequence: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != "1" || event.EventIndex != 1 ||
		event.Event.Type != string(domain.EventAccountFunded) || event.Event.Amount != "1" {
		t.Fatalf("event envelope = %+v", event)
	}
}

type oneEventSource struct {
	event tradingserver.StoredEvent
}

func (s *oneEventSource) EventsAfter(
	_ context.Context,
	cursor tradingserver.Cursor,
	_ int,
) ([]tradingserver.StoredEvent, error) {
	if cursor.Sequence == 0 {
		return []tradingserver.StoredEvent{s.event}, nil
	}
	return nil, nil
}

func (s *oneEventSource) BatchEventCount(
	_ context.Context,
	sequence uint64,
) (uint32, bool, error) {
	if sequence != s.event.Cursor.Sequence {
		return 0, false, nil
	}
	return s.event.BatchEventCount, true, nil
}

func newTestClient(
	t *testing.T,
	events tradingserver.EventSource,
) (tradingv1.TradingServiceClient, *tradingruntime.MarketRunner) {
	t.Helper()
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
	service, err := tradingserver.New(runner, events, tradingserver.Config{
		EventBatchSize: 10,
		EventPollEvery: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, service)
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := grpc.DialContext(
		ctx,
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		grpcServer.Stop()
		_ = listener.Close()
		closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if err := runner.Close(closeContext); err != nil {
			t.Errorf("close runner: %v", err)
		}
	})
	return tradingv1.NewTradingServiceClient(connection), runner
}

func mustFund(
	t *testing.T,
	ctx context.Context,
	client tradingv1.TradingServiceClient,
	requestID string,
	accountID string,
	asset string,
	amount string,
) {
	t.Helper()
	if _, err := client.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{
		MarketId:  "BTC-USDT",
		RequestId: requestID,
		AccountId: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertBalance(
	t *testing.T,
	balances []*tradingv1.Balance,
	asset string,
	available string,
	held string,
) {
	t.Helper()
	for _, balance := range balances {
		if balance.Asset == asset {
			if balance.Available != available || balance.Held != held {
				t.Fatalf("balance %s = %+v", asset, balance)
			}
			return
		}
	}
	t.Fatalf("balance %s is missing: %+v", asset, balances)
}
