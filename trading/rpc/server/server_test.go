package server_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	"github.com/the-web3/s78-market-services/trading/recovery"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

type liquidityProvider struct{ status marketmaker.LiquidityStatus }

func (p liquidityProvider) Status() marketmaker.LiquidityStatus { return p.status }

func TestPausedLiquidityRejectsSubmitButAllowsCancelAndFund(t *testing.T) {
	ctx := context.Background()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		ctx, domain.DefaultBTCUSDTMarket(), memory, memory, tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	if _, err := runner.Fund(ctx, domain.FundRequest{
		RequestID: "seed", AccountID: "alice", Asset: "USDT", Amount: 1_000_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	open, err := runner.Submit(ctx, domain.NewOrder{
		ClientOrderID: "open-before-pause", AccountID: "alice", Side: domain.SideBuy,
		Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC,
		Price: 60_000_000_000, Quantity: 100_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pausedAt := time.Now().UTC()
	config := tradingserver.DefaultConfig()
	config.VirtualLiquidity = liquidityProvider{status: marketmaker.LiquidityStatus{
		Provider: marketmaker.VirtualLiquidityProvider, State: marketmaker.LiquidityPaused,
		Reason: "reference is unavailable", LastRefreshAt: pausedAt,
	}}
	server, err := tradingserver.New(runner, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := server.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId: "BTC-USDT", AccountId: "alice", ClientOrderId: "open-before-pause",
		Side: tradingv1.Side_SIDE_BUY, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:       "60000", Quantity: "0.001",
	})
	if err != nil || replay.Sequence != strconv.FormatUint(open.Sequence, 10) || replay.OrderId != string(open.OrderID) {
		t.Fatalf("paused exact replay=%+v err=%v", replay, err)
	}
	_, err = server.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId: "BTC-USDT", AccountId: "alice", ClientOrderId: "open-before-pause",
		Side: tradingv1.Side_SIDE_BUY, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:       "59999", Quantity: "0.001",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("paused conflicting replay error=%v", err)
	}
	_, err = server.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
		MarketId: "BTC-USDT", AccountId: "alice", ClientOrderId: "blocked",
		Side: tradingv1.Side_SIDE_BUY, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:       "60000", Quantity: "0.001",
	})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "liquidity_paused") {
		t.Fatalf("submit error = %v", err)
	}
	if _, err := server.CancelOrder(ctx, &tradingv1.CancelOrderRequest{
		MarketId: "BTC-USDT", AccountId: "alice", RequestId: "cancel-during-pause",
		OrderId: string(open.OrderID),
	}); err != nil {
		t.Fatalf("cancel during liquidity pause: %v", err)
	}
	if _, err := server.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{
		MarketId: "BTC-USDT", AccountId: "alice", RequestId: "fund-during-pause",
		Asset: "BTC", Amount: "0.1",
	}); err != nil {
		t.Fatalf("fund during liquidity pause: %v", err)
	}
	got, err := server.GetStatus(ctx, &tradingv1.GetStatusRequest{MarketId: "BTC-USDT"})
	if err != nil || got.GetVirtualLiquidity().GetState() != "paused" ||
		got.GetVirtualLiquidity().GetProvider() != "Qiu Virtual Liquidity" ||
		got.GetVirtualLiquidity().GetLastRefreshAt() != pausedAt.Format(time.RFC3339Nano) {
		t.Fatalf("status=%+v err=%v", got.GetVirtualLiquidity(), err)
	}
}

func TestVirtualLiquidityFreshnessBoundaryIsAuthoritativeForSubmit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		age  time.Duration
		want codes.Code
	}{
		{name: "29s", age: 29 * time.Second, want: codes.OK},
		{name: "30s", age: 30 * time.Second, want: codes.OK},
		{name: "31s", age: 31 * time.Second, want: codes.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			memory := store.NewMemory()
			runner, err := tradingruntime.NewMarketRunner(ctx, domain.DefaultBTCUSDTMarket(), memory, memory, tradingruntime.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = runner.Close(context.Background()) }()
			if _, err := runner.Fund(ctx, domain.FundRequest{RequestID: "fund", AccountID: "alice", Asset: "USDT", Amount: 100_000_000_000}); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Fund(ctx, domain.FundRequest{RequestID: "maker-btc", AccountID: "system:demo-maker", Asset: "BTC", Amount: 1_000_000}); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Fund(ctx, domain.FundRequest{RequestID: "maker-usdt", AccountID: "system:demo-maker", Asset: "USDT", Amount: 100_000_000_000}); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Submit(ctx, domain.NewOrder{ClientOrderID: "maker-ask", AccountID: "system:demo-maker", Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, Price: 61_000_000_000, Quantity: 1_000_000, PostOnly: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Submit(ctx, domain.NewOrder{ClientOrderID: "maker-bid", AccountID: "system:demo-maker", Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, Price: 59_000_000_000, Quantity: 1_000_000, PostOnly: true}); err != nil {
				t.Fatal(err)
			}
			config := tradingserver.DefaultConfig()
			config.Now = func() time.Time { return now }
			config.VirtualLiquidity = liquidityProvider{status: marketmaker.LiquidityStatus{
				Provider: marketmaker.VirtualLiquidityProvider, State: marketmaker.LiquidityActive,
				BidLevels: 1, AskLevels: 1, ReferenceObservedAt: now.Add(-test.age), LastRefreshAt: now,
			}}
			server, err := tradingserver.New(runner, nil, config)
			if err != nil {
				t.Fatal(err)
			}
			_, err = server.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{
				MarketId: "BTC-USDT", AccountId: "alice", ClientOrderId: "age-" + test.name,
				Side: tradingv1.Side_SIDE_BUY, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
				TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC, Price: "60000", Quantity: "0.001", PostOnly: true,
			})
			if status.Code(err) != test.want {
				t.Fatalf("age=%s submit code=%s err=%v", test.age, status.Code(err), err)
			}
			if test.want == codes.Unavailable {
				open, openErr := runner.Submit(ctx, domain.NewOrder{ClientOrderID: "cancel-target", AccountID: "alice", Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, Price: 59_000_000_000, Quantity: 100_000, PostOnly: true})
				if openErr != nil {
					t.Fatal(openErr)
				}
				if _, cancelErr := server.CancelOrder(ctx, &tradingv1.CancelOrderRequest{MarketId: "BTC-USDT", AccountId: "alice", RequestId: "cancel-stale", OrderId: string(open.OrderID)}); cancelErr != nil {
					t.Fatalf("cancel at stale reference: %v", cancelErr)
				}
			}
		})
	}
}

func TestRecoveryOperatorRPCBindsAndPromotesAuthoritativeCoordinator(t *testing.T) {
	ctx := context.Background()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
		testRecoveryProvenance(),
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		ctx, domain.DefaultBTCUSDTMarket(), memory, memory,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	runtimeStatus := runner.Status()
	proof := recovery.Proof{
		RuntimeSequence: runtimeStatus.Sequence, StateHash: runtimeStatus.StateHash,
		LedgerBalanced: true, EventContinuous: true,
		ProjectionCaughtUp: true, OutboxCaughtUp: true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady, recovery.PhaseTradingReplay,
		recovery.PhaseReconciling, recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		current, err = coordinator.Advance(ctx, phase, proof)
		if err != nil {
			t.Fatal(err)
		}
	}
	config := tradingserver.DefaultConfig()
	config.Recovery = coordinator
	service, err := tradingserver.New(runner, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	statusResponse, err := service.GetRecoveryStatus(ctx, &tradingv1.GetRecoveryStatusRequest{
		MarketId: "BTC-USDT",
	})
	if err != nil || statusResponse.GetEpochId() != current.EpochID ||
		statusResponse.GetVersion() != strconv.FormatUint(current.Version, 10) {
		t.Fatalf("operator status = %+v err=%v", statusResponse, err)
	}
	last := time.Now().UTC()
	request := &tradingv1.PromoteRecoveryRequest{
		Binding: &tradingv1.RecoveryBinding{
			MarketId: "BTC-USDT", EpochId: current.EpochID,
			Version:         strconv.FormatUint(current.Version, 10),
			RuntimeSequence: strconv.FormatUint(current.Proof.RuntimeSequence, 10),
			StateHash:       current.Proof.StateHash,
			Provenance:      testProtoProvenance(),
		},
		TransportEvidence: &tradingv1.RecoveryTransportEvidence{
			SampleCount:    recovery.MinimumTransportSamples,
			FirstSampleAt:  last.Add(-recovery.MinimumTransportWindow).Format(time.RFC3339Nano),
			LastSampleAt:   last.Format(time.RFC3339Nano),
			MaximumGapMs:   strconv.FormatInt(recovery.MaximumTransportGap.Milliseconds(), 10),
			EvidenceSha256: strings.Repeat("b", 64),
			Provenance:     testProtoProvenance(),
		},
	}
	promoted, err := service.PromoteRecovery(ctx, request)
	if err != nil || !promoted.GetWritesEnabled() || promoted.GetPhase() != "writable" {
		t.Fatalf("operator promotion = %+v err=%v", promoted, err)
	}
	if _, err := service.PromoteRecovery(ctx, request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale operator promotion error = %v", err)
	}
}

func TestPromoteRecoveryRejectsLiveRuntimeDivergence(t *testing.T) {
	ctx := context.Background()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
		testRecoveryProvenance(),
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		ctx, domain.DefaultBTCUSDTMarket(), memory, memory,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	observed := runner.Status()
	proof := recovery.Proof{
		RuntimeSequence: observed.Sequence, StateHash: observed.StateHash,
		LedgerBalanced: true, EventContinuous: true,
		ProjectionCaughtUp: true, OutboxCaughtUp: true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady, recovery.PhaseTradingReplay,
		recovery.PhaseReconciling, recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		current, err = coordinator.Advance(ctx, phase, proof)
		if err != nil {
			t.Fatal(err)
		}
	}

	config := tradingserver.DefaultConfig()
	config.Recovery = coordinator
	service, err := tradingserver.New(runner, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Fund(ctx, domain.FundRequest{
		RequestID: "post-observation-fund",
		AccountID: "user",
		Asset:     "USDT",
		Amount:    1_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	last := time.Now().UTC()
	_, err = service.PromoteRecovery(ctx, &tradingv1.PromoteRecoveryRequest{
		Binding: &tradingv1.RecoveryBinding{
			MarketId: "BTC-USDT", EpochId: current.EpochID,
			Version:         strconv.FormatUint(current.Version, 10),
			RuntimeSequence: strconv.FormatUint(observed.Sequence, 10),
			StateHash:       observed.StateHash,
			Provenance:      testProtoProvenance(),
		},
		TransportEvidence: &tradingv1.RecoveryTransportEvidence{
			SampleCount:    recovery.MinimumTransportSamples,
			FirstSampleAt:  last.Add(-recovery.MinimumTransportWindow).Format(time.RFC3339Nano),
			LastSampleAt:   last.Format(time.RFC3339Nano),
			MaximumGapMs:   strconv.FormatInt(recovery.MaximumTransportGap.Milliseconds(), 10),
			EvidenceSha256: strings.Repeat("b", 64),
			Provenance:     testProtoProvenance(),
		},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("runtime divergence promotion error = %v", err)
	}
	after, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Phase != recovery.PhaseTransportWarmup || after.WritesEnabled {
		t.Fatalf("runtime divergence changed recovery state = %+v", after)
	}
}

func testRecoveryProvenance() recovery.Provenance {
	return recovery.Provenance{
		ProductionOrigin: "https://qiu-market.example", DeploymentID: "dpl_rpctest00",
		DeploymentURL: "https://qiu-market-rpc-test.vercel.app",
		ReleaseCommit: strings.Repeat("d", 40), SourceDigest: strings.Repeat("e", 64),
	}
}

func testProtoProvenance() *tradingv1.RecoveryProvenance {
	value := testRecoveryProvenance()
	return &tradingv1.RecoveryProvenance{
		ProductionOrigin: value.ProductionOrigin, DeploymentId: value.DeploymentID,
		DeploymentUrl: value.DeploymentURL,
		ReleaseCommit: value.ReleaseCommit, SourceDigest: value.SourceDigest,
	}
}

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
		MarketId: "BTC-USDT",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(trades.Trades) != 1 || trades.Trades[0].Price != "60000" ||
		trades.Trades[0].Quantity != "0.07" {
		t.Fatalf("trades = %+v", trades.Trades)
	}
	if trades.Trades[0].MakerAccountId != "" || trades.Trades[0].TakerAccountId != "" ||
		trades.Trades[0].BuyerAccountId != "" || trades.Trades[0].SellerAccountId != "" ||
		trades.Trades[0].BuyerFee.GetAccountId() != "" ||
		trades.Trades[0].SellerFee.GetAccountId() != "" {
		t.Fatalf("public ListTrades leaked account identity: %+v", trades.Trades[0])
	}
	if _, err := client.ListTrades(ctx, &tradingv1.ListTradesRequest{
		MarketId: "BTC-USDT", AccountId: "taker",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("public ListTrades accepted account_id: %v", err)
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
