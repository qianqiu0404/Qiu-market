package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/outbox"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

const browserOrigin = "http://trade.test"

func TestVirtualSpotTransportTradeFeesCancelAndRestart(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	testID := strconv.FormatInt(time.Now().UnixNano(), 10)
	market := domain.DefaultBTCUSDTMarket()
	market.ID = domain.MarketID("BTC-USDT-E2E-" + testID)
	userAccount := "e2e:qianqiu0404:" + testID
	makerAccount := "system:demo-maker"

	adminPool := openPool(t, dsn)
	if err := postgresstore.EnsureSchema(context.Background(), adminPool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupFixture(t, adminPool, market.ID, userAccount)
		adminPool.Close()
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browserClient := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	first := startStack(t, dsn, market, userAccount)
	defer first.stop(t)

	var login struct {
		Principal auth.Principal `json:"principal"`
	}
	doJSON(t, browserClient, http.MethodPost,
		first.http.URL+"/api/v1/trading/auth/local",
		map[string]any{}, "", http.StatusOK, &login)
	if login.Principal.AccountID != userAccount || !login.Principal.Admin {
		t.Fatalf("local principal = %+v", login.Principal)
	}
	csrf := cookieValue(t, jar, first.http.URL, "s78_trading_csrf")

	fundBody := map[string]any{
		"request_id": "fund-browser-user",
		"asset":      "USDT",
		"amount":     "10000",
	}
	postJSONAndLoseCommittedResponse(t, browserClient.Jar,
		first.http.URL+"/api/v1/trading/admin/fund", fundBody, csrf)

	var fundedRetry tradingv1.CommandResult
	doJSON(t, browserClient, http.MethodPost,
		first.http.URL+"/api/v1/trading/admin/fund",
		fundBody, csrf, http.StatusOK, &fundedRetry)
	if fundedRetry.Sequence != "1" || fundedRetry.Status != "unknown" {
		t.Fatalf("same-ID funding retry = %+v, want sequence 1 fund status", &fundedRetry)
	}
	if _, err := first.client.AdminFundVirtual(context.Background(),
		&tradingv1.AdminFundVirtualRequest{
			MarketId:  string(market.ID),
			RequestId: "fund-demo-maker",
			AccountId: makerAccount,
			Asset:     "BTC",
			Amount:    "0.1",
		}); err != nil {
		t.Fatal(err)
	}
	maker, err := first.client.SubmitOrder(context.Background(),
		&tradingv1.SubmitOrderRequest{
			MarketId:      string(market.ID),
			AccountId:     makerAccount,
			ClientOrderId: "demo-maker-sell",
			Side:          tradingv1.Side_SIDE_SELL,
			Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
			TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
			Price:         "60000",
			Quantity:      "0.1",
			PostOnly:      true,
		})
	if err != nil || maker.Sequence != "3" || maker.Status != "open" {
		t.Fatalf("maker result = %+v, error = %v", maker, err)
	}

	ws := connectEvents(t, browserClient, first.http.URL, csrf, "3", "0")
	var taker tradingv1.CommandResult
	takerBody := map[string]any{
		"client_order_id": "browser-taker-buy",
		"side":            "buy",
		"type":            "limit",
		"time_in_force":   "ioc",
		"post_only":       false,
		"price":           "60200",
		"quantity":        "0.07",
		"quote_budget":    "",
	}
	doJSON(t, browserClient, http.MethodPost,
		first.http.URL+"/api/v1/trading/orders",
		takerBody, csrf, http.StatusOK, &taker)
	if taker.Sequence != "4" || taker.Status != "filled" {
		t.Fatalf("taker result = %+v", &taker)
	}
	assertTradeArrived(t, ws, "4", userAccount)
	_ = ws.Close()

	var privateTrades tradingv1.ListTradesResponse
	doJSON(t, browserClient, http.MethodGet,
		first.http.URL+"/api/v1/trading/trades?limit=100",
		nil, "", http.StatusOK, &privateTrades)
	assertTrade(t, privateTrades.Trades, userAccount, makerAccount)

	canceled, err := first.client.CancelOrder(context.Background(),
		&tradingv1.CancelOrderRequest{
			MarketId:  string(market.ID),
			AccountId: makerAccount,
			RequestId: "cancel-demo-maker-rest",
			OrderId:   maker.OrderId,
		})
	if err != nil || canceled.Sequence != "5" || canceled.Status != "canceled" {
		t.Fatalf("cancel result = %+v, error = %v", canceled, err)
	}
	assertBalancesBeforeRestart(t, browserClient, first, market.ID, userAccount, makerAccount)
	assertPlatformFeeLedger(t, adminPool, market.ID)

	first.stop(t)
	before := loadRecoveryProof(t, adminPool, market.ID)
	if before.currentSequence != 5 || before.snapshotSequence != 5 ||
		before.snapshotHash == "" || before.snapshotHash != before.eventHash {
		t.Fatalf("pre-restart proof = %+v", before)
	}

	second := startStack(t, dsn, market, userAccount)
	defer second.stop(t)

	var session struct {
		Principal auth.Principal `json:"principal"`
	}
	doJSON(t, browserClient, http.MethodGet,
		second.http.URL+"/api/v1/trading/session",
		nil, "", http.StatusOK, &session)
	if session.Principal.AccountID != userAccount {
		t.Fatalf("session did not survive restart: %+v", session.Principal)
	}
	var statusResponse tradingv1.StatusResponse
	doJSON(t, browserClient, http.MethodGet,
		second.http.URL+"/api/v1/trading/markets/"+string(market.ID)+"/status",
		nil, "", http.StatusOK, &statusResponse)
	if statusResponse.State != "ready" || statusResponse.Sequence != "5" {
		t.Fatalf("restored status = %+v", &statusResponse)
	}

	csrf = cookieValue(t, jar, second.http.URL, "s78_trading_csrf")
	var restoredFundRetry tradingv1.CommandResult
	doJSON(t, browserClient, http.MethodPost,
		second.http.URL+"/api/v1/trading/admin/fund",
		fundBody, csrf, http.StatusOK, &restoredFundRetry)
	if restoredFundRetry.Sequence != fundedRetry.Sequence ||
		restoredFundRetry.Status != fundedRetry.Status {
		t.Fatalf("cross-restart funding retry = %+v, want %+v",
			&restoredFundRetry, &fundedRetry)
	}
	assertVirtualFundAppliedOnce(
		t, adminPool, market.ID, userAccount, "fund-browser-user", 10_000_000_000,
	)

	var idempotentRetry tradingv1.CommandResult
	doJSON(t, browserClient, http.MethodPost,
		second.http.URL+"/api/v1/trading/orders",
		takerBody, csrf, http.StatusOK, &idempotentRetry)
	if idempotentRetry.Sequence != "4" || idempotentRetry.Status != "filled" {
		t.Fatalf("cross-restart idempotent retry = %+v", &idempotentRetry)
	}
	assertBalancesBeforeRestart(t, browserClient, second, market.ID, userAccount, makerAccount)
	assertPlatformFeeLedger(t, adminPool, market.ID)

	privateTrades = tradingv1.ListTradesResponse{}
	doJSON(t, browserClient, http.MethodGet,
		second.http.URL+"/api/v1/trading/trades?limit=100",
		nil, "", http.StatusOK, &privateTrades)
	assertTrade(t, privateTrades.Trades, userAccount, makerAccount)

	second.stop(t)
	after := loadRecoveryProof(t, adminPool, market.ID)
	if after != before {
		t.Fatalf("restart changed durable state: before=%+v after=%+v", before, after)
	}
}

type runningStack struct {
	pool       *pgxpool.Pool
	runner     *tradingruntime.MarketRunner
	grpcServer *grpc.Server
	grpcConn   *grpc.ClientConn
	listener   net.Listener
	http       *httptest.Server
	client     tradingv1.TradingServiceClient
	outboxStop context.CancelFunc
	outboxDone chan struct{}
	stopOnce   sync.Once
}

func startStack(
	t *testing.T,
	dsn string,
	market domain.Market,
	localAccount string,
) *runningStack {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := openPool(t, dsn)
	if err := postgresstore.EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	runner, err := tradingruntime.NewMarketRunner(
		ctx,
		market,
		persistence,
		persistence,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	rpcService, err := tradingserver.New(
		runner,
		tradingserver.NewPostgresEventSource(persistence),
		tradingserver.DefaultConfig(),
	)
	if err != nil {
		closeRunner(t, runner)
		pool.Close()
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		closeRunner(t, runner)
		pool.Close()
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, rpcService)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	grpcConn, err := grpc.DialContext(
		ctx,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		grpcServer.Stop()
		_ = listener.Close()
		closeRunner(t, runner)
		pool.Close()
		t.Fatal(err)
	}
	sessions, err := auth.NewPostgresSessionStore(pool, "qianqiu0404")
	if err != nil {
		grpcConn.Close()
		grpcServer.Stop()
		_ = listener.Close()
		closeRunner(t, runner)
		pool.Close()
		t.Fatal(err)
	}
	tickets, err := auth.NewTicketManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oauthStates, err := auth.NewOAuthStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	httpConfig := httpapi.DefaultConfig()
	httpConfig.MarketID = string(market.ID)
	httpConfig.BindAddress = "127.0.0.1:0"
	httpConfig.AllowedOrigins = []string{browserOrigin}
	httpConfig.LocalMode = true
	httpConfig.LocalAccountID = localAccount
	httpConfig.WriteLimit = 1_000
	httpServer, err := httpapi.New(
		tradingv1.NewTradingServiceClient(grpcConn),
		sessions,
		tickets,
		oauthStates,
		nil,
		httpConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	outboxConfig := outbox.DefaultConfig()
	outboxConfig.PollEvery = 10 * time.Millisecond
	publisher, err := outbox.New(persistence, outboxConfig)
	if err != nil {
		t.Fatal(err)
	}
	outboxContext, outboxStop := context.WithCancel(context.Background())
	outboxDone := make(chan struct{})
	go func() {
		publisher.Run(outboxContext)
		close(outboxDone)
	}()
	transport := httptest.NewServer(httpServer.Handler())
	return &runningStack{
		pool:       pool,
		runner:     runner,
		grpcServer: grpcServer,
		grpcConn:   grpcConn,
		listener:   listener,
		http:       transport,
		client:     tradingv1.NewTradingServiceClient(grpcConn),
		outboxStop: outboxStop,
		outboxDone: outboxDone,
	}
}

func (s *runningStack) stop(t *testing.T) {
	t.Helper()
	s.stopOnce.Do(func() {
		s.http.Close()
		s.outboxStop()
		<-s.outboxDone
		_ = s.grpcConn.Close()
		s.grpcServer.GracefulStop()
		_ = s.listener.Close()
		closeRunner(t, s.runner)
		s.pool.Close()
	})
}

func closeRunner(t *testing.T, runner *tradingruntime.MarketRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Close(ctx); err != nil {
		t.Errorf("close runner: %v", err)
	}
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool
}

func connectEvents(
	t *testing.T,
	client *http.Client,
	baseURL, csrf, sequence, eventIndex string,
) *websocket.Conn {
	t.Helper()
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	doJSON(t, client, http.MethodPost,
		baseURL+"/api/v1/trading/ws-ticket",
		map[string]any{}, csrf, http.StatusCreated, &ticket)
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") +
		"/api/v1/trading/events/ws?ticket=" + url.QueryEscape(ticket.Ticket) +
		"&sequence=" + url.QueryEscape(sequence) +
		"&event_index=" + url.QueryEscape(eventIndex)
	header := http.Header{"Origin": {browserOrigin}}
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
		}
		t.Fatalf("connect event websocket: %v", err)
	}
	return connection
}

func assertTradeArrived(
	t *testing.T,
	connection *websocket.Conn,
	sequence, userAccount string,
) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var envelope tradingv1.EventEnvelope
		if err := connection.ReadJSON(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Sequence != sequence || envelope.Event == nil ||
			envelope.Event.Trade == nil {
			continue
		}
		if envelope.Event.Trade.BuyerAccountId != userAccount {
			t.Fatalf("WebSocket trade = %+v", envelope.Event.Trade)
		}
		return
	}
}

func assertTrade(
	t *testing.T,
	trades []*tradingv1.Trade,
	buyer, seller string,
) {
	t.Helper()
	if len(trades) != 1 {
		t.Fatalf("trades = %+v", trades)
	}
	trade := trades[0]
	if trade.Price != "60000" || trade.Quantity != "0.07" ||
		trade.QuoteAmount != "4200" ||
		trade.BuyerAccountId != buyer || trade.SellerAccountId != seller {
		t.Fatalf("trade settlement = %+v", trade)
	}
	if trade.BuyerFee == nil || trade.BuyerFee.Amount != "0.00014" ||
		trade.BuyerFee.Asset != "BTC" || trade.BuyerFee.RateBps != "20" ||
		trade.BuyerFee.Role != "taker" {
		t.Fatalf("buyer fee = %+v", trade.BuyerFee)
	}
	if trade.SellerFee == nil || trade.SellerFee.Amount != "4.2" ||
		trade.SellerFee.Asset != "USDT" || trade.SellerFee.RateBps != "10" ||
		trade.SellerFee.Role != "maker" {
		t.Fatalf("seller fee = %+v", trade.SellerFee)
	}
}

func assertBalancesBeforeRestart(
	t *testing.T,
	httpClient *http.Client,
	stack *runningStack,
	marketID domain.MarketID,
	userAccount, makerAccount string,
) {
	t.Helper()
	var userBalances tradingv1.GetBalancesResponse
	doJSON(t, httpClient, http.MethodGet,
		stack.http.URL+"/api/v1/trading/balances",
		nil, "", http.StatusOK, &userBalances)
	assertBalance(t, userBalances.Balances, "BTC", "0.06986", "0")
	assertBalance(t, userBalances.Balances, "USDT", "5800", "0")

	makerBalances, err := stack.client.GetBalances(context.Background(),
		&tradingv1.GetBalancesRequest{
			MarketId: string(marketID), AccountId: makerAccount,
		})
	if err != nil {
		t.Fatal(err)
	}
	assertBalance(t, makerBalances.Balances, "BTC", "0.03", "0")
	assertBalance(t, makerBalances.Balances, "USDT", "4195.8", "0")

	book, err := stack.client.GetOrderBook(context.Background(),
		&tradingv1.GetOrderBookRequest{MarketId: string(marketID), Levels: 20})
	if err != nil {
		t.Fatal(err)
	}
	if book.Sequence != "5" || len(book.Bids) != 0 || len(book.Asks) != 0 {
		t.Fatalf("book after cancel = %+v", book)
	}
}

func assertPlatformFeeLedger(
	t *testing.T,
	pool *pgxpool.Pool,
	marketID domain.MarketID,
) {
	t.Helper()
	for _, expected := range []struct {
		account string
		asset   domain.Asset
		amount  int64
	}{
		{ledger.PlatformFee(domain.Asset("BTC")), domain.Asset("BTC"), 14_000},
		{ledger.PlatformFee(domain.Asset("USDT")), domain.Asset("USDT"), 4_200_000},
	} {
		var amount int64
		if err := pool.QueryRow(context.Background(), `
			SELECT COALESCE(SUM(amount), 0)
			FROM trading_ledger_entry
			WHERE market_id=$1 AND account=$2 AND asset=$3
		`, marketID, expected.account, expected.asset).Scan(&amount); err != nil {
			t.Fatal(err)
		}
		if amount != expected.amount {
			t.Fatalf("%s/%s ledger fee = %d, want %d",
				expected.account, expected.asset, amount, expected.amount)
		}
	}
}

func assertVirtualFundAppliedOnce(
	t *testing.T,
	pool *pgxpool.Pool,
	marketID domain.MarketID,
	accountID, requestID string,
	amount int64,
) {
	t.Helper()
	var eventBatches int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM trading_event_batch
		WHERE market_id=$1
		  AND operation=$2
		  AND account_id=$3
		  AND request_id=$4
	`, marketID, int16(domain.CommandKindFund), accountID, requestID).Scan(
		&eventBatches,
	); err != nil {
		t.Fatal(err)
	}
	if eventBatches != 1 {
		t.Fatalf("fund event batches = %d, want 1", eventBatches)
	}

	var (
		entries   int
		netAmount int64
		userEntry int64
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COUNT(*),
			COALESCE(SUM(amount), 0),
			COALESCE(SUM(amount) FILTER (WHERE account=$3), 0)
		FROM trading_ledger_entry
		WHERE market_id=$1 AND sequence=$2
	`, marketID, int64(1), ledger.UserAvailable(domain.AccountID(accountID))).Scan(
		&entries, &netAmount, &userEntry,
	); err != nil {
		t.Fatal(err)
	}
	if entries != 2 || netAmount != 0 || userEntry != amount {
		t.Fatalf(
			"fund ledger entries/net/user = %d/%d/%d, want 2/0/%d",
			entries, netAmount, userEntry, amount,
		)
	}
}

func assertBalance(
	t *testing.T,
	balances []*tradingv1.Balance,
	asset, available, held string,
) {
	t.Helper()
	for _, balance := range balances {
		if balance.Asset != asset {
			continue
		}
		if balance.Available != available || balance.Held != held {
			t.Fatalf("%s balance = %+v, want available=%s held=%s",
				asset, balance, available, held)
		}
		return
	}
	t.Fatalf("%s balance is missing: %+v", asset, balances)
}

type recoveryProof struct {
	currentSequence  int64
	snapshotSequence int64
	snapshotHash     string
	eventHash        string
}

func loadRecoveryProof(
	t *testing.T,
	pool *pgxpool.Pool,
	marketID domain.MarketID,
) recoveryProof {
	t.Helper()
	var result recoveryProof
	err := pool.QueryRow(context.Background(), `
		SELECT m.current_sequence, s.sequence, s.state_hash, e.state_hash
		FROM trading_market m
		JOIN trading_snapshot s ON s.market_id=m.market_id
		JOIN LATERAL (
			SELECT state_hash
			FROM trading_event_batch
			WHERE market_id=m.market_id
			ORDER BY sequence DESC
			LIMIT 1
		) e ON true
		WHERE m.market_id=$1
	`, marketID).Scan(
		&result.currentSequence,
		&result.snapshotSequence,
		&result.snapshotHash,
		&result.eventHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func cleanupFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	marketID domain.MarketID,
	accountID string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Errorf("begin E2E cleanup: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM trading_user_session WHERE account_id=$1`,
		accountID,
	); err != nil {
		t.Errorf("clean E2E session: %v", err)
		return
	}
	for _, table := range []string{
		"trading_projection_checkpoint",
		"trading_outbox_checkpoint",
		"trading_ledger_entry",
		"trading_balance",
		"trading_trade",
		"trading_order",
		"trading_event_feed",
		"trading_outbox",
		"trading_snapshot",
		"trading_event_batch",
		"trading_market",
	} {
		if _, err := tx.Exec(ctx,
			`DELETE FROM `+table+` WHERE market_id=$1`,
			marketID,
		); err != nil {
			t.Errorf("clean E2E table %s: %v", table, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit E2E cleanup: %v", err)
	}
}

func doJSON(
	t *testing.T,
	client *http.Client,
	method, target string,
	body any,
	csrf string,
	expectedStatus int,
	destination any,
) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(
		context.Background(), method, target, reader,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", browserOrigin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s %s status=%d body=%s",
			method, target, response.StatusCode, data)
	}
	if destination != nil && len(data) > 0 {
		if err := json.Unmarshal(data, destination); err != nil {
			t.Fatalf("decode %s %s: %v body=%s", method, target, err, data)
		}
	}
}

type loseCommittedResponseTransport struct {
	base http.RoundTripper
}

func (t loseCommittedResponseTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return nil, errors.New("simulated response loss after server commit")
}

func postJSONAndLoseCommittedResponse(
	t *testing.T,
	jar http.CookieJar,
	target string,
	body any,
	csrf string,
) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		target,
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", browserOrigin)
	request.Header.Set("X-CSRF-Token", csrf)
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: loseCommittedResponseTransport{
			base: http.DefaultTransport,
		},
	}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "simulated response loss") {
		t.Fatalf("response-loss injection error = %v", err)
	}
}

func cookieValue(
	t *testing.T,
	jar http.CookieJar,
	rawURL, name string,
) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("cookie %s is missing", name)
	return ""
}
