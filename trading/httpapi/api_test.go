package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestHTTPAuthCSRFAccountIsolationPublicRedactionAndTicket(t *testing.T) {
	grpcClient, _ := newGRPCTestClient(t)
	sessions := newMemorySessions()
	tickets, err := auth.NewTicketManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oauthStates, err := auth.NewOAuthStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	config := httpapi.DefaultConfig()
	config.LocalMode = true
	config.AllowedOrigins = []string{"http://trade.test"}
	config.WriteLimit = 100
	api, err := httpapi.New(grpcClient, sessions, tickets, oauthStates, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	response := do(t, client, http.MethodGet,
		httpServer.URL+"/api/v1/trading/auth/capabilities", nil, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("auth capabilities status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var capabilities map[string]bool
	decodeResponse(t, response, &capabilities)
	if !capabilities["local_login_enabled"] || capabilities["github_oauth_enabled"] {
		t.Fatalf("auth capabilities = %+v", capabilities)
	}

	response = do(t, client, http.MethodGet,
		httpServer.URL+"/api/v1/trading/markets/BTC-USDT/orderbook", nil, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public order book status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var emptyBook map[string]json.RawMessage
	decodeResponse(t, response, &emptyBook)
	if string(emptyBook["bids"]) != "[]" || string(emptyBook["asks"]) != "[]" {
		t.Fatalf("empty order book must expose stable arrays: %s", emptyBook)
	}
	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/auth/local", map[string]any{}, "", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("originless local login status = %d: %s", response.StatusCode, readBody(t, response))
	}
	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/auth/local", map[string]any{}, "http://trade.test", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("local login status = %d: %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
	csrfToken := cookieValue(t, jar, httpServer.URL, "s78_trading_csrf")

	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/admin/fund",
		map[string]any{"request_id": "missing-csrf", "asset": "BTC", "amount": "0.2"},
		"http://trade.test", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d: %s", response.StatusCode, readBody(t, response))
	}
	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/admin/fund",
		map[string]any{"request_id": "fund-self", "asset": "BTC", "amount": "0.2"},
		"http://trade.test", csrfToken)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fund status = %d: %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()

	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/orders",
		map[string]any{
			"account_id":      "forged-account",
			"client_order_id": "self-sell",
			"side":            "sell",
			"type":            "limit",
			"time_in_force":   "gtc",
			"price":           "61000",
			"quantity":        "0.1",
		},
		"http://trade.test", csrfToken)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit status = %d: %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
	response = do(t, client, http.MethodGet,
		httpServer.URL+"/api/v1/trading/orders", nil, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list orders status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var orders tradingv1.ListOrdersResponse
	decodeResponse(t, response, &orders)
	if len(orders.Orders) != 1 || orders.Orders[0].AccountId != "github:qianqiu0404" {
		t.Fatalf("forged account was not ignored: %+v", orders.Orders)
	}

	mustFundGRPC(t, grpcClient, "fund-victim", "victim", "BTC", "0.2")
	victimOrder, err := grpcClient.SubmitOrder(context.Background(), &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "victim",
		ClientOrderId: "victim-sell",
		Side:          tradingv1.Side_SIDE_SELL,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:         "62000",
		Quantity:      "0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/orders/"+victimOrder.OrderId+"/cancel",
		map[string]any{"account_id": "victim", "request_id": "forged-cancel"},
		"http://trade.test", csrfToken)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forged cancel status = %d: %s", response.StatusCode, readBody(t, response))
	}

	mustFundGRPC(t, grpcClient, "fund-public-maker", "public-maker", "BTC", "0.1")
	mustFundGRPC(t, grpcClient, "fund-public-taker", "public-taker", "USDT", "10000")
	publicMaker, err := grpcClient.SubmitOrder(context.Background(), &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "public-maker",
		ClientOrderId: "public-maker-sell",
		Side:          tradingv1.Side_SIDE_SELL,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_GTC,
		Price:         "60000",
		Quantity:      "0.05",
	})
	if err != nil || publicMaker.Status != "open" {
		t.Fatalf("public maker = %+v, %v", publicMaker, err)
	}
	if _, err := grpcClient.SubmitOrder(context.Background(), &tradingv1.SubmitOrderRequest{
		MarketId:      "BTC-USDT",
		AccountId:     "public-taker",
		ClientOrderId: "public-taker-buy",
		Side:          tradingv1.Side_SIDE_BUY,
		Type:          tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce:   tradingv1.TimeInForce_TIME_IN_FORCE_IOC,
		Price:         "60000",
		Quantity:      "0.05",
	}); err != nil {
		t.Fatal(err)
	}
	response = do(t, client, http.MethodGet,
		httpServer.URL+"/api/v1/trading/markets/BTC-USDT/trades", nil, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("public trades status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var trades tradingv1.ListTradesResponse
	decodeResponse(t, response, &trades)
	if len(trades.Trades) == 0 ||
		trades.Trades[0].MakerAccountId != "" || trades.Trades[0].TakerAccountId != "" ||
		trades.Trades[0].BuyerAccountId != "" || trades.Trades[0].SellerAccountId != "" {
		t.Fatalf("public trade leaked accounts: %+v", trades.Trades)
	}

	response = do(t, client, http.MethodPost,
		httpServer.URL+"/api/v1/trading/ws-ticket", map[string]any{},
		"http://trade.test", csrfToken)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("ticket status = %d: %s", response.StatusCode, readBody(t, response))
	}
	var ticketResponse struct {
		Ticket string `json:"ticket"`
	}
	decodeResponse(t, response, &ticketResponse)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") +
		"/api/v1/trading/events/ws?ticket=" + url.QueryEscape(ticketResponse.Ticket)
	header := http.Header{"Origin": {"http://trade.test"}}
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("first ticket dial: %v", err)
	}
	_ = connection.Close()
	_, response, err = websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused ticket dial = response:%v error:%v", response, err)
	}
	_ = response.Body.Close()

	sessions.expireAll()
	response = do(t, client, http.MethodGet,
		httpServer.URL+"/api/v1/trading/balances", nil, "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d: %s", response.StatusCode, readBody(t, response))
	}
}

type memorySessions struct {
	mu       sync.Mutex
	sessions map[string]auth.Session
}

func newMemorySessions() *memorySessions {
	return &memorySessions{sessions: make(map[string]auth.Session)}
}

func (s *memorySessions) Create(
	_ context.Context,
	principal auth.Principal,
	ttl time.Duration,
) (auth.Credentials, error) {
	sessionToken, err := auth.NewToken()
	if err != nil {
		return auth.Credentials{}, err
	}
	csrfToken, err := auth.NewToken()
	if err != nil {
		return auth.Credentials{}, err
	}
	csrfHash := sha256.Sum256([]byte(csrfToken))
	expiresAt := time.Now().Add(ttl)
	s.mu.Lock()
	s.sessions[sessionToken] = auth.Session{
		Principal: principal,
		CSRFHash:  csrfHash,
		ExpiresAt: expiresAt,
	}
	s.mu.Unlock()
	return auth.Credentials{
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func (s *memorySessions) Lookup(
	_ context.Context,
	token string,
) (auth.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[token]
	if !exists || !session.ExpiresAt.After(time.Now()) {
		return auth.Session{}, false, nil
	}
	return session, true, nil
}

func (s *memorySessions) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	return nil
}

func (s *memorySessions) expireAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]auth.Session)
}

func newGRPCTestClient(
	t *testing.T,
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
	service, err := tradingserver.New(runner, nil, tradingserver.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(server, service)
	go func() {
		_ = server.Serve(listener)
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
		server.Stop()
		_ = listener.Close()
		closeContext, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if err := runner.Close(closeContext); err != nil {
			t.Errorf("close runner: %v", err)
		}
	})
	return tradingv1.NewTradingServiceClient(connection), runner
}

func mustFundGRPC(
	t *testing.T,
	client tradingv1.TradingServiceClient,
	requestID string,
	accountID string,
	asset string,
	amount string,
) {
	t.Helper()
	if _, err := client.AdminFundVirtual(context.Background(), &tradingv1.AdminFundVirtualRequest{
		MarketId:  "BTC-USDT",
		RequestId: requestID,
		AccountId: accountID,
		Asset:     asset,
		Amount:    amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func do(
	t *testing.T,
	client *http.Client,
	method string,
	target string,
	body any,
	origin string,
	csrf string,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func cookieValue(
	t *testing.T,
	jar http.CookieJar,
	rawURL string,
	name string,
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

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var _ auth.SessionStore = (*memorySessions)(nil)
