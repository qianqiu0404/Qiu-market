package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/gateway"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

const (
	practiceTestOrigin = "http://127.0.0.1:15174"
	practiceCursorKey  = "fixture:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func TestPracticeDeterministicMakerPartialCancelReplayAndRestart(t *testing.T) {
	stateDSN := os.Getenv("QIU_T1_CLOSURE_STATE_DSN")
	referenceDSN := os.Getenv("QIU_T1_TEST_REFERENCE_DSN")
	if stateDSN == "" || referenceDSN == "" {
		t.Skip("QIU_T1_CLOSURE_STATE_DSN and QIU_T1_TEST_REFERENCE_DSN are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	first := startPracticeClosureStack(t, ctx, stateDSN, referenceDSN, true)

	var login struct {
		Principal auth.Principal `json:"principal"`
	}
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/auth/local", map[string]any{}, "", http.StatusOK, &login)
	if login.Principal.AccountID != "github:qianqiu0404" || !login.Principal.Admin {
		t.Fatalf("local principal=%+v", login.Principal)
	}
	csrf := practiceCookie(t, jar, first.server.URL, "s78_trading_csrf")
	starterUSDT := map[string]any{
		"request_id": "starter-v1-usdt", "asset": "USDT", "amount": "10000",
	}
	starterBTC := map[string]any{
		"request_id": "starter-v1-btc", "asset": "BTC", "amount": "0.1",
	}
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/admin/fund", starterUSDT, csrf, http.StatusOK, &tradingv1.CommandResult{})
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/admin/fund", starterBTC, csrf, http.StatusOK, &tradingv1.CommandResult{})

	var status tradingv1.StatusResponse
	deadline := time.Now().Add(10 * time.Second)
	for {
		practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/markets/BTC-USDT/status", nil, "", http.StatusOK, &status)
		if status.GetVirtualLiquidity().GetState() == "active" &&
			status.GetVirtualLiquidity().GetBidLevels() == 3 &&
			status.GetVirtualLiquidity().GetAskLevels() == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("virtual liquidity never became active: %+v", status.GetVirtualLiquidity())
		}
		time.Sleep(50 * time.Millisecond)
	}
	var book tradingv1.OrderBook
	practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/markets/BTC-USDT/orderbook", nil, "", http.StatusOK, &book)
	assertPracticeQuoteLadder(t, &book)

	orderBody := map[string]any{
		"client_order_id": "practice-user-buy-v1", "side": "buy", "type": "limit",
		"time_in_force": "gtc", "post_only": false, "price": "60300", "quantity": "0.04",
	}
	var submitted tradingv1.CommandResult
	if statusCode, payload := practiceRequest(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/orders", orderBody, csrf, &submitted); statusCode != http.StatusOK {
		t.Fatalf("submit status=%d body=%s runner=%+v", statusCode, payload, first.backend.runner.Status())
	}
	if submitted.Status != "partially_filled" || submitted.OrderId == "" {
		t.Fatalf("partial result=%+v", &submitted)
	}
	var order tradingv1.Order
	practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/orders/"+submitted.OrderId, nil, "", http.StatusOK, &order)
	if order.FilledQuantity != "0.03" || order.RemainingQuantity != "0.01" || order.HeldAmount != "603" {
		t.Fatalf("partial order=%+v", &order)
	}
	var trades tradingv1.ListAccountTradesResponse
	practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/account/trades?limit=100", nil, "", http.StatusOK, &trades)
	assertPracticeTrades(t, trades.Trades)
	closureDeadline := time.Now().Add(10 * time.Second)
	for {
		liquidity := first.backend.liquidityStatus.Status()
		if liquidity.State == marketmaker.LiquidityRecovering && strings.Contains(liquidity.Reason, "resting user order") {
			break
		}
		if time.Now().After(closureDeadline) {
			t.Fatalf("maker did not enter recoverable quote-blocked state: %+v", liquidity)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !first.backend.makerRunning() {
		t.Fatal("maker goroutine terminated while user remainder blocked post-only quotes")
	}

	cancelBody := map[string]any{"request_id": "practice-user-cancel-v1"}
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/orders/"+submitted.OrderId+"/cancel", cancelBody, csrf, http.StatusOK, &tradingv1.CommandResult{})
	practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/orders/"+submitted.OrderId, nil, "", http.StatusOK, &order)
	if order.Status != "canceled" || order.HeldAmount != "0" || order.HeldAsset != "" {
		t.Fatalf("canceled order=%+v", &order)
	}
	closureDeadline = time.Now().Add(10 * time.Second)
	for {
		practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/markets/BTC-USDT/status", nil, "", http.StatusOK, &status)
		practiceJSON(t, client, http.MethodGet, first.server.URL+"/api/v1/trading/markets/BTC-USDT/orderbook", nil, "", http.StatusOK, &book)
		if status.GetVirtualLiquidity().GetState() == "active" && len(book.Bids) == 3 && len(book.Asks) == 3 {
			break
		}
		if time.Now().After(closureDeadline) {
			t.Fatalf("maker did not replenish six levels after user cancel: liquidity=%+v book=%+v", status.GetVirtualLiquidity(), &book)
		}
		time.Sleep(50 * time.Millisecond)
	}

	pool, err := pgxpool.New(ctx, stateDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	assertPracticeLedgerBalanced(t, ctx, pool)
	assertStarterFundingTruth(t, ctx, pool)
	factsBefore := practiceFactCounts(t, ctx, pool)
	var submitReplay, cancelReplay tradingv1.CommandResult
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/orders", orderBody, csrf, http.StatusOK, &submitReplay)
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/orders/"+submitted.OrderId+"/cancel", cancelBody, csrf, http.StatusOK, &cancelReplay)
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/admin/fund", starterUSDT, csrf, http.StatusOK, &tradingv1.CommandResult{})
	practiceJSON(t, client, http.MethodPost, first.server.URL+"/api/v1/trading/admin/fund", starterBTC, csrf, http.StatusOK, &tradingv1.CommandResult{})
	if submitReplay.Sequence != submitted.Sequence || submitReplay.OrderId != submitted.OrderId || cancelReplay.Status != "canceled" {
		t.Fatalf("idempotent replay submit=%+v cancel=%+v", &submitReplay, &cancelReplay)
	}
	if after := practiceFactCounts(t, ctx, pool); after != factsBefore {
		t.Fatalf("same-ID replay mutated durable facts before=%+v after=%+v", factsBefore, after)
	}

	stopMakerCtx, stopMakerCancel := context.WithTimeout(ctx, 10*time.Second)
	if err := first.backend.stopMaker(stopMakerCtx, false); err != nil {
		stopMakerCancel()
		t.Fatal(err)
	}
	stopMakerCancel()
	before := capturePracticeViews(t, client, first.server.URL, submitted.OrderId)
	first.stop(t)

	second := startPracticeClosureStack(t, ctx, stateDSN, referenceDSN, false)
	defer second.stop(t)
	after := capturePracticeViews(t, client, second.server.URL, submitted.OrderId)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("restart changed balances/orders/trades/timeline/cursors/state hash\nbefore=%+v\nafter=%+v", before, after)
	}
}

type practiceClosureStack struct {
	backend *Backend
	gateway *gateway.Gateway
	server  *httptest.Server
	cancel  context.CancelFunc
}

func startPracticeClosureStack(t *testing.T, parent context.Context, stateDSN, referenceDSN string, maker bool) *practiceClosureStack {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	backend, err := New(ctx, Config{
		StatePostgresURL: stateDSN, ReferencePostgresURL: referenceDSN,
		GRPCAddress: "127.0.0.1:0", PracticeMode: true, DemoMakerEnabled: maker,
		CursorHMACCurrent: practiceCursorKey,
	}, func(error) { cancel() })
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := backend.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	tradingGateway, err := gateway.New(ctx, gateway.Config{
		PostgresURL: stateDSN, PracticeMode: true, VirtualLiquidityEnabled: maker,
		GRPCAddress: backend.GRPCAddress(), BindAddress: "127.0.0.1:0",
		AllowedOrigins: []string{practiceTestOrigin}, LocalAuth: true, SecureCookies: false,
	})
	if err != nil {
		stopPracticeBackend(t, backend)
		cancel()
		t.Fatal(err)
	}
	return &practiceClosureStack{backend: backend, gateway: tradingGateway, server: httptest.NewServer(tradingGateway.Handler()), cancel: cancel}
}

func (s *practiceClosureStack) stop(t *testing.T) {
	t.Helper()
	if s.backend == nil {
		return
	}
	s.cancel()
	s.server.Close()
	if err := s.gateway.Close(); err != nil {
		t.Error(err)
	}
	stopPracticeBackend(t, s.backend)
	s.backend = nil
}

func stopPracticeBackend(t *testing.T, backend *Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := backend.Stop(ctx); err != nil {
		t.Error(err)
	}
}

func assertPracticeQuoteLadder(t *testing.T, book *tradingv1.OrderBook) {
	t.Helper()
	if len(book.Bids) != 3 || len(book.Asks) != 3 {
		t.Fatalf("six quote ladder=%+v", &book)
	}
	wantAsks := []string{"60060", "60150", "60300"}
	for index, level := range book.Asks {
		if level.Price != wantAsks[index] || level.Quantity != "0.01" || level.OrderCount != 1 {
			t.Fatalf("ask[%d]=%+v", index, level)
		}
	}
}

func assertPracticeTrades(t *testing.T, trades []*tradingv1.AccountTrade) {
	t.Helper()
	if len(trades) != 3 {
		t.Fatalf("taker trades=%+v", trades)
	}
	prices := make([]string, 0, 3)
	for _, trade := range trades {
		prices = append(prices, trade.Price)
		if trade.Quantity != "0.01" || trade.Side != "buy" || trade.LiquidityRole != "taker" ||
			trade.FeeAsset != "BTC" || trade.FeeAmount != "0.00002" || trade.FeeRateBps != "20" {
			t.Fatalf("trade=%+v", trade)
		}
	}
	sort.Strings(prices)
	if !reflect.DeepEqual(prices, []string{"60060", "60150", "60300"}) {
		t.Fatalf("trade prices=%v", prices)
	}
}

func assertPracticeLedgerBalanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var unbalanced, undersized int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT transaction_id, asset FROM trading_ledger_entry
			WHERE market_id='BTC-USDT' GROUP BY transaction_id, asset HAVING sum(amount) <> 0
		) bad
	`).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT transaction_id FROM trading_ledger_entry
			WHERE market_id='BTC-USDT' GROUP BY transaction_id HAVING count(*) < 2
		) bad
	`).Scan(&undersized); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 || undersized != 0 {
		t.Fatalf("double-entry ledger unbalanced=%d undersized=%d", unbalanced, undersized)
	}
}

func assertStarterFundingTruth(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT request_id, count(*), min(sequence), max(sequence)
		FROM trading_event_batch
		WHERE market_id='BTC-USDT' AND operation=1
		  AND account_id='github:qianqiu0404'
		  AND request_id IN ('starter-v1-usdt','starter-v1-btc')
		GROUP BY request_id ORDER BY request_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var requestID string
		var count, minimum, maximum int
		if err := rows.Scan(&requestID, &count, &minimum, &maximum); err != nil {
			t.Fatal(err)
		}
		if count != 1 || minimum != maximum || (requestID != "starter-v1-usdt" && requestID != "starter-v1-btc") {
			t.Fatalf("starter funding truth id=%s count=%d sequence=%d/%d", requestID, count, minimum, maximum)
		}
		seen++
	}
	if err := rows.Err(); err != nil || seen != 2 {
		t.Fatalf("starter funding rows=%d err=%v", seen, err)
	}
}

type practiceCounts struct{ Events, Trades, Entries int }

func practiceFactCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) practiceCounts {
	t.Helper()
	var result practiceCounts
	if err := pool.QueryRow(ctx, `
		SELECT
		 (SELECT count(*) FROM trading_event_batch WHERE market_id='BTC-USDT'),
		 (SELECT count(*) FROM trading_trade WHERE market_id='BTC-USDT'),
		 (SELECT count(*) FROM trading_ledger_entry WHERE market_id='BTC-USDT')
	`).Scan(&result.Events, &result.Trades, &result.Entries); err != nil {
		t.Fatal(err)
	}
	return result
}

type practiceViews struct {
	Balances, Orders, Trades, Timeline, Ledger string
	StateHash                                  string
}

func capturePracticeViews(t *testing.T, client *http.Client, baseURL, orderID string) practiceViews {
	t.Helper()
	var status tradingv1.StatusResponse
	practiceJSON(t, client, http.MethodGet, baseURL+"/api/v1/trading/markets/BTC-USDT/status", nil, "", http.StatusOK, &status)
	return practiceViews{
		Balances:  practiceBody(t, client, baseURL+"/api/v1/trading/balances"),
		Orders:    practiceBody(t, client, baseURL+"/api/v1/trading/orders?limit=100"),
		Trades:    practiceBody(t, client, baseURL+"/api/v1/trading/account/trades?limit=100"),
		Timeline:  practiceBody(t, client, baseURL+"/api/v1/trading/orders/"+orderID+"/events?limit=100"),
		Ledger:    practiceBody(t, client, baseURL+"/api/v1/trading/ledger/entries?limit=100"),
		StateHash: status.StateHash,
	}
}

func practiceBody(t *testing.T, client *http.Client, endpoint string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d err=%v body=%s", endpoint, response.StatusCode, err, payload)
	}
	return string(payload)
}

func practiceJSON(t *testing.T, client *http.Client, method, endpoint string, body any, csrf string, want int, destination any) {
	t.Helper()
	statusCode, payload := practiceRequest(t, client, method, endpoint, body, csrf, destination)
	if statusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, endpoint, statusCode, want, payload)
	}
}

func practiceRequest(t *testing.T, client *http.Client, method, endpoint string, body any, csrf string, destination any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", practiceTestOrigin)
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
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if destination != nil && len(payload) > 0 && response.StatusCode < 400 {
		if err := json.Unmarshal(payload, destination); err != nil {
			t.Fatalf("decode %s: %v body=%s", endpoint, err, payload)
		}
	}
	return response.StatusCode, payload
}

func practiceCookie(t *testing.T, jar http.CookieJar, endpoint, name string) string {
	t.Helper()
	parsed, err := url.Parse(endpoint)
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
