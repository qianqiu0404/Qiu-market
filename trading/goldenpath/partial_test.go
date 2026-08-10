package goldenpath

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"testing"
	"time"
)

func TestPartialGoldenProductionHTTPRestartAndCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	running, err := StartPartial(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelClose()
		if closeErr := running.Close(closeCtx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/auth/local", DefaultPartialFrontendOrigin, "", map[string]any{}, http.StatusOK)
	csrf := cookieValue(client, running.URL, "s78_trading_csrf")
	if csrf == "" {
		t.Fatal("missing CSRF cookie")
	}
	orderBody := map[string]any{"client_order_id": "partial-browser-buyer-v1", "side": "buy", "type": "limit", "time_in_force": "gtc", "post_only": true, "price": PartialPrice, "quantity": PartialBuyerQuantity}
	submitted := requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/orders", DefaultPartialFrontendOrigin, csrf, orderBody, http.StatusOK)
	if submitted["status"] != "open" {
		t.Fatalf("submit=%v", submitted)
	}
	orderID, ok := submitted["order_id"].(string)
	if !ok || orderID == "" {
		t.Fatalf("missing order ID: %v", submitted)
	}
	partial := requestJSON(t, client, http.MethodPost, running.URL+"/__partial-golden/partial-fill", "", "", map[string]any{"resting_client_order_id": "partial-browser-buyer-v1"}, http.StatusOK)
	if partial["status"] != "partially_filled" {
		t.Fatalf("partial=%v", partial)
	}
	before, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPartialState(t, before, "partially_filled", 4, 1, 5, 14, "800", "600", "0.00999", "0")
	if before.Order == nil || before.Order.ID != orderID || before.Order.FilledQuantity != "0.01" || before.Order.RemainingQuantity != "0.01" || before.Order.HeldAsset != "USDT" || before.Order.HeldAmount != "600" {
		t.Fatalf("partial order=%+v", before.Order)
	}

	restart := requestJSON(t, client, http.MethodPost, running.URL+"/__partial-golden/restart", "", "", map[string]any{}, http.StatusOK)
	if restart["snapshot_found"] != true || restart["unchanged"] != true || restart["record_count_before"] != float64(4) || restart["record_count_after"] != float64(4) || restart["before_sequence"] != float64(4) || restart["after_sequence"] != float64(4) || restart["snapshot_sequence"] != float64(4) {
		t.Fatalf("restart proof=%v", restart)
	}
	after, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RestartCount != 1 || !after.RestartProof.Unchanged || after.RestartProof.SnapshotHash != before.StateHash || after.StateHash != before.StateHash {
		t.Fatalf("restored state=%+v before=%+v", after, before)
	}
	before.RestartCount = after.RestartCount
	before.RestartProof = after.RestartProof
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("restart changed state:\nbefore=%+v\nafter=%+v", before, after)
	}

	canceled := requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/orders/"+orderID+"/cancel", DefaultPartialFrontendOrigin, csrf, map[string]any{"request_id": "partial-browser-cancel-v1"}, http.StatusOK)
	if canceled["status"] != "canceled" {
		t.Fatalf("cancel=%v", canceled)
	}
	final, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPartialState(t, final, "canceled", 5, 1, 6, 16, "1400", "0", "0.00999", "0")
	if final.Order == nil || final.Order.HeldAsset != "" || final.Order.HeldAmount != "0" {
		t.Fatalf("canceled order=%+v", final.Order)
	}
	ledgerResponse := requestJSON(t, client, http.MethodGet, running.URL+"/api/v1/trading/ledger/entries?limit=100", "", "", nil, http.StatusOK)
	entries, ok := ledgerResponse["entries"].([]any)
	if !ok || len(entries) != 7 {
		t.Fatalf("buyer ledger entries=%v", ledgerResponse)
	}
	foundRelease := false
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["reference"] == "order-cancel:"+orderID && entry["order_id"] == orderID && entry["reason"] == "order_release" {
			foundRelease = true
		}
	}
	if !foundRelease {
		t.Fatalf("cancel release reference absent: %v", entries)
	}
	requestJSON(t, client, http.MethodPost, running.URL+"/api/v1/trading/orders/"+orderID+"/cancel", DefaultPartialFrontendOrigin, csrf, map[string]any{"request_id": "partial-browser-cancel-v1"}, http.StatusOK)
	replayed, err := running.Harness.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.FactCount != final.FactCount || replayed.ReplayEvidence.CancelReplays != 1 {
		t.Fatalf("cancel replay mutated facts: %+v", replayed)
	}
}

func assertPartialState(t *testing.T, state PartialState, status string, facts, trades, transactions, entries int, buyerAvailable, buyerHeld, buyerBTC, buyerBTCHeld string) {
	t.Helper()
	if state.FactCount != uint64(facts) || state.BuyerTrades != trades || state.Ledger.TransactionCount != transactions || state.Ledger.EntryCount != entries || !state.Ledger.Balanced || state.DuplicateTransactions || state.JournalSums["BTC"] != "0" || state.JournalSums["USDT"] != "0" {
		t.Fatalf("state evidence=%+v", state)
	}
	if state.Order == nil || state.Order.Status != status {
		t.Fatalf("order state=%+v", state.Order)
	}
	assertBalance(t, state.BuyerBalances, "USDT", buyerAvailable, buyerHeld)
	assertBalance(t, state.BuyerBalances, "BTC", buyerBTC, buyerBTCHeld)
}
