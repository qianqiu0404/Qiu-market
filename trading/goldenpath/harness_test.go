package goldenpath

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

func TestGoldenPathThroughProductionHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	running, err := Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if closeErr := running.Close(closeCtx); closeErr != nil {
			t.Errorf("close harness: %v", closeErr)
		}
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	origin := DefaultFrontendOrigin
	requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/auth/local", origin, "", map[string]any{}, http.StatusOK)
	csrf := cookieValue(client, running.URL, "s78_trading_csrf")
	if csrf == "" {
		t.Fatal("local login did not issue CSRF cookie")
	}

	order := map[string]any{
		"account_id":      "must-be-ignored",
		"client_order_id": "golden-buyer-order-v1",
		"side":            "buy",
		"type":            "limit",
		"time_in_force":   "gtc",
		"post_only":       true,
		"price":           Price,
		"quantity":        Quantity,
	}
	first := requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/orders", origin, csrf, order, http.StatusOK)
	if got := first["status"]; got != "open" {
		t.Fatalf("buyer order status = %v, want open", got)
	}
	openState, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if openState.FactCount != 3 || openState.BuyerTrades != 0 || openState.BuyerBalances["USDT"] != (FormattedBalance{Available: "400", Held: "600"}) {
		t.Fatalf("unexpected resting-order state: %+v", openState)
	}

	replayed := requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/orders", origin, csrf, order, http.StatusOK)
	if replayed["order_id"] != first["order_id"] || replayed["sequence"] != first["sequence"] {
		t.Fatalf("idempotent response changed: first=%v replay=%v", first, replayed)
	}
	replayState, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replayState.FactCount != openState.FactCount {
		t.Fatalf("idempotent replay added facts: before=%d after=%d", openState.FactCount, replayState.FactCount)
	}
	if replayState.ReplayEvidence.OrderReplays != 1 {
		t.Fatalf("order replay evidence = %+v", replayState.ReplayEvidence)
	}

	fillBody := map[string]any{"resting_client_order_id": "golden-buyer-order-v1"}
	fill := requestJSON(t, client, http.MethodPost, running.URL+"/__golden/fill", "", "", fillBody, http.StatusOK)
	if got := fill["status"]; got != "filled" {
		t.Fatalf("seller order status = %v, want filled", got)
	}
	filledState, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if filledState.FactCount != 4 || filledState.BuyerTrades != 1 {
		t.Fatalf("unexpected filled state: %+v", filledState)
	}
	assertBalance(t, filledState.BuyerBalances, "BTC", "0.00999", "0")
	assertBalance(t, filledState.BuyerBalances, "USDT", "400", "0")
	assertBalance(t, filledState.SellerBalances, "BTC", "0", "0")
	assertBalance(t, filledState.SellerBalances, "USDT", "598.8", "0")
	if filledState.PlatformFees["BTC"] != "0.00001" || filledState.PlatformFees["USDT"] != "1.2" {
		t.Fatalf("platform fees = %#v", filledState.PlatformFees)
	}
	if !filledState.Ledger.Balanced || filledState.JournalSums["BTC"] != "0" || filledState.JournalSums["USDT"] != "0" || filledState.Ledger.TransactionCount != 5 || filledState.Ledger.EntryCount != 14 {
		t.Fatalf("ledger evidence = %+v sums=%v", filledState.Ledger, filledState.JournalSums)
	}

	orders := requestJSON(t, client, http.MethodGet, running.URL+"/api/v1/trading/orders?scope=all&limit=100", "", "", nil, http.StatusOK)
	assertListLength(t, orders, "orders", 1)
	trades := requestJSON(t, client, http.MethodGet, running.URL+"/api/v1/trading/account/trades?limit=100", "", "", nil, http.StatusOK)
	assertListLength(t, trades, "trades", 1)
	orderView := firstListObject(t, orders, "orders")
	tradeView := firstListObject(t, trades, "trades")
	if orderView["id"] != first["order_id"] || tradeView["order_id"] != first["order_id"] || tradeView["liquidity_role"] != "maker" {
		t.Fatalf("order/trade identities are inconsistent: submit=%v order=%v trade=%v", first, orderView, tradeView)
	}
	tradeID, ok := tradeView["id"].(string)
	if !ok || tradeID == "" {
		t.Fatalf("trade response has no identity: %v", tradeView)
	}
	balances := requestJSON(t, client, http.MethodGet, running.URL+"/api/v1/trading/balances", "", "", nil, http.StatusOK)
	assertListLength(t, balances, "balances", 2)
	ledger := requestJSON(t, client, http.MethodGet, running.URL+"/api/v1/trading/ledger/entries?limit=100", "", "", nil, http.StatusOK)
	entries, ok := ledger["entries"].([]any)
	if !ok || len(entries) < 4 {
		t.Fatalf("ledger response missing account entries: %v", ledger)
	}
	var foundHold, foundTrade bool
	for _, raw := range entries {
		entry, entryOK := raw.(map[string]any)
		if !entryOK {
			t.Fatalf("invalid ledger entry: %v", raw)
		}
		if entry["reference"] == "order-hold:"+first["order_id"].(string) && entry["order_id"] == first["order_id"] {
			foundHold = true
		}
		if entry["reference"] == "matched-trade:"+tradeID && entry["trade_id"] == tradeID && entry["reason"] == "trade_settlement" {
			foundTrade = true
		}
	}
	if !foundHold || !foundTrade {
		t.Fatalf("ledger references do not link order/trade: hold=%v trade=%v entries=%v", foundHold, foundTrade, entries)
	}

	beforeSecondFill := running.Harness.store.RecordCount()
	requestJSON(t, client, http.MethodPost, running.URL+"/__golden/fill", "", "", fillBody, http.StatusOK)
	if after := running.Harness.store.RecordCount(); after != beforeSecondFill {
		t.Fatalf("fill replay added facts: before=%d after=%d", beforeSecondFill, after)
	}
	ready := requestJSON(t, client, http.MethodGet, running.URL+"/__golden/ready", "", "", nil, http.StatusOK)
	if ready["ready"] != true {
		t.Fatalf("ready response = %v", ready)
	}
	finalState, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finalState.ReplayEvidence.FillReplays != 1 {
		t.Fatalf("fill replay evidence = %+v", finalState.ReplayEvidence)
	}
}

func TestFillFailsClosedWithoutMatchingOpenBuyerOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close(context.Background()) })
	client := &http.Client{Timeout: time.Second}
	requestJSON(t, client, http.MethodPost, running.URL+"/__golden/fill", "", "", map[string]any{"resting_client_order_id": "missing"}, http.StatusConflict)
	state, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.FactCount != 2 || state.BuyerTrades != 0 {
		t.Fatalf("failed control mutated state: %+v", state)
	}
}

func TestDeterministicMarketDataEnvelopes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := Start(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close(context.Background()) })
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/api/v2/get_asset_dashboard", "/api/v2/get_asset_markets", "/api/v2/get_asset_venues", "/api/v1/get_klines"} {
		response := requestJSON(t, client, http.MethodPost, running.URL+path, "", "", map[string]any{}, http.StatusOK)
		if response["code"] != float64(2000) {
			t.Fatalf("%s envelope = %v", path, response)
		}
		want := 1
		if path == "/api/v1/get_klines" {
			want = 2
		}
		assertListLength(t, response, "result", want)
	}
	price := requestJSON(t, client, http.MethodGet, running.URL+"/__golden/market-data/reference", "", "", nil, http.StatusOK)
	observedAt, err := time.Parse(time.RFC3339Nano, price["observed_at"].(string))
	if err != nil || time.Since(observedAt) > time.Minute {
		t.Fatalf("reference price not fresh: %v", price)
	}
}

func TestLoopbackOnlyRejectsNonLoopbackPeer(t *testing.T) {
	handler := loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "http://golden.test/__golden/ready", nil)
	request.RemoteAddr = "203.0.113.9:4242"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestFilledResultGate(t *testing.T) {
	for _, test := range []struct {
		name   string
		result *tradingv1.CommandResult
		want   bool
	}{
		{name: "nil", result: nil, want: false},
		{name: "open", result: &tradingv1.CommandResult{Status: "open"}, want: false},
		{name: "filled", result: &tradingv1.CommandResult{Status: "filled"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isFilledResult(test.result); got != test.want {
				t.Fatalf("isFilledResult() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFixedRestingBuyerOrderRequiresExactQuoteHold(t *testing.T) {
	market := domain.DefaultBTCUSDTMarket()
	order := &domain.Order{
		AccountID: BuyerAccount, Status: domain.OrderStatusOpen, Side: domain.SideBuy, Type: domain.OrderTypeLimit,
		Price: 60_000_000_000, OriginalQuantity: 1_000_000, RemainingQuantity: 1_000_000,
		HeldAsset: market.QuoteAsset, HeldAmount: 600_000_000,
	}
	if !isFixedRestingBuyerOrder(order, market, 60_000_000_000, 1_000_000, 600_000_000) {
		t.Fatal("exact fixed resting order was rejected")
	}
	wrongAsset := *order
	wrongAsset.HeldAsset = market.BaseAsset
	if isFixedRestingBuyerOrder(&wrongAsset, market, 60_000_000_000, 1_000_000, 600_000_000) {
		t.Fatal("resting order with wrong held asset was accepted")
	}
	wrongAmount := *order
	wrongAmount.HeldAmount--
	if isFixedRestingBuyerOrder(&wrongAmount, market, 60_000_000_000, 1_000_000, 600_000_000) {
		t.Fatal("resting order with wrong held amount was accepted")
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url, origin, csrf string, body any, wantStatus int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
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
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode %s %s: %v", method, url, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status=%d want=%d response=%v", method, url, response.StatusCode, wantStatus, decoded)
	}
	return decoded
}

func cookieValue(client *http.Client, rawURL, name string) string {
	request, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	for _, cookie := range client.Jar.Cookies(request.URL) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func assertBalance(t *testing.T, balances map[string]FormattedBalance, asset, available, held string) {
	t.Helper()
	if got := balances[asset]; got != (FormattedBalance{Available: available, Held: held}) {
		t.Fatalf("%s balance=%+v, want available=%s held=%s", asset, got, available, held)
	}
}

func assertListLength(t *testing.T, response map[string]any, field string, want int) {
	t.Helper()
	items, ok := response[field].([]any)
	if !ok || len(items) != want {
		t.Fatalf("%s response=%v, want length %d", field, response, want)
	}
}

func firstListObject(t *testing.T, response map[string]any, field string) map[string]any {
	t.Helper()
	items, ok := response[field].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("%s response has no items: %v", field, response)
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("%s first item is invalid: %v", field, items[0])
	}
	return item
}
