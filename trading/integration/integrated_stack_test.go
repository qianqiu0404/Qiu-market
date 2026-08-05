package integration_test

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
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/gateway"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingservice "github.com/the-web3/s78-market-services/trading/service"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

const (
	integratedMarketID = "BTC-USDT"
	integratedOrigin   = "http://trade.integration.test"
)

func TestCanonicalMigrationIntegratedGatewayAndRestartRecovery(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	if os.Getenv("S78_TEST_POSTGRES_ISOLATED") != "1" {
		t.Skip("integrated stack test requires S78_TEST_POSTGRES_ISOLATED=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openPool(t, ctx, dsn)
	t.Cleanup(pool.Close)
	applyCanonicalMigration(t, ctx, pool)
	if err := postgresstore.VerifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var existing bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM trading_market WHERE market_id=$1)`,
		integratedMarketID,
	).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing {
		t.Fatal("integrated stack test refuses to reuse an existing BTC-USDT stream")
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanupMarket(t, cleanupContext, pool)
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	first := startIntegratedStack(t, ctx, dsn)
	loginURL := first.server.URL + "/api/v1/trading/auth/local"
	var login struct {
		Principal auth.Principal `json:"principal"`
	}
	doJSON(t, client, http.MethodPost, loginURL, map[string]any{}, "", http.StatusOK, &login)
	if login.Principal.AccountID != "github:qianqiu0404" || !login.Principal.Admin {
		t.Fatalf("local principal = %+v", login.Principal)
	}
	csrf := cookieValue(t, jar, first.server.URL, "s78_trading_csrf")

	var funded tradingv1.CommandResult
	doJSON(t, client, http.MethodPost,
		first.server.URL+"/api/v1/trading/admin/fund",
		map[string]any{
			"request_id": "integrated-fund-usdt",
			"asset":      "USDT",
			"amount":     "100",
		},
		csrf, http.StatusOK, &funded)
	if funded.Sequence != "1" {
		t.Fatalf("fund sequence = %s", funded.Sequence)
	}

	var submitted tradingv1.CommandResult
	doJSON(t, client, http.MethodPost,
		first.server.URL+"/api/v1/trading/orders",
		map[string]any{
			"account_id":      "forged:account",
			"client_order_id": "integrated-open-order",
			"side":            "buy",
			"type":            "limit",
			"time_in_force":   "gtc",
			"post_only":       true,
			"price":           "60000",
			"quantity":        "0.001",
		},
		csrf, http.StatusOK, &submitted)
	if submitted.Sequence != "2" || submitted.Status != "open" || submitted.OrderId == "" {
		t.Fatalf("submit result = %+v", &submitted)
	}

	var canceled tradingv1.CommandResult
	cancelBody := map[string]any{
		"account_id": "forged:account",
		"request_id": "integrated-cancel-order",
	}
	doJSON(t, client, http.MethodPost,
		first.server.URL+"/api/v1/trading/orders/"+submitted.OrderId+"/cancel",
		cancelBody, csrf, http.StatusOK, &canceled)
	if canceled.Sequence != "3" || canceled.Status != "canceled" {
		t.Fatalf("cancel result = %+v", &canceled)
	}

	first.stop(t)
	before := recoveryProofFor(t, ctx, pool)
	if before.Sequence != 3 || before.SnapshotSequence != 3 ||
		before.EventHash == "" || before.EventHash != before.SnapshotHash {
		t.Fatalf("pre-restart proof = %+v", before)
	}

	second := startIntegratedStack(t, ctx, dsn)
	defer second.stop(t)
	var session struct {
		Principal auth.Principal `json:"principal"`
	}
	doJSON(t, client, http.MethodGet,
		second.server.URL+"/api/v1/trading/session",
		nil, "", http.StatusOK, &session)
	if session.Principal.AccountID != login.Principal.AccountID {
		t.Fatalf("session did not survive integrated restart: %+v", session.Principal)
	}

	var status tradingv1.StatusResponse
	doJSON(t, client, http.MethodGet,
		second.server.URL+"/api/v1/trading/markets/BTC-USDT/status",
		nil, "", http.StatusOK, &status)
	if status.State != "ready" || status.Sequence != "3" {
		t.Fatalf("restored status = %+v", &status)
	}

	csrf = cookieValue(t, jar, second.server.URL, "s78_trading_csrf")
	var retry tradingv1.CommandResult
	doJSON(t, client, http.MethodPost,
		second.server.URL+"/api/v1/trading/orders/"+submitted.OrderId+"/cancel",
		cancelBody, csrf, http.StatusOK, &retry)
	if retry.Sequence != canceled.Sequence || retry.Status != canceled.Status {
		t.Fatalf("cross-restart idempotent retry = %+v, want %+v", &retry, &canceled)
	}

	second.stop(t)
	after := recoveryProofFor(t, ctx, pool)
	if after != before {
		t.Fatalf("integrated restart changed durable state: before=%+v after=%+v", before, after)
	}
}

type integratedStack struct {
	backend *tradingservice.Backend
	gateway *gateway.Gateway
	server  *httptest.Server
	cancel  context.CancelFunc
}

func startIntegratedStack(t *testing.T, parent context.Context, dsn string) *integratedStack {
	t.Helper()
	appContext, appCancel := context.WithCancel(parent)
	backend, err := tradingservice.New(appContext, tradingservice.Config{
		PostgresURL:      dsn,
		GRPCAddress:      "127.0.0.1:0",
		DemoMakerEnabled: false,
	}, func(error) { appCancel() })
	if err != nil {
		appCancel()
		t.Fatal(err)
	}
	if err := backend.Start(appContext); err != nil {
		appCancel()
		t.Fatal(err)
	}
	tradingGateway, err := gateway.New(appContext, gateway.Config{
		PostgresURL:    dsn,
		GRPCAddress:    backend.GRPCAddress(),
		BindAddress:    "127.0.0.1:0",
		AllowedOrigins: []string{integratedOrigin},
		LocalAuth:      true,
		SecureCookies:  false,
	})
	if err != nil {
		stopBackend(t, backend)
		appCancel()
		t.Fatal(err)
	}
	server := httptest.NewServer(tradingGateway.Handler())
	return &integratedStack{
		backend: backend,
		gateway: tradingGateway,
		server:  server,
		cancel:  appCancel,
	}
}

func (s *integratedStack) stop(t *testing.T) {
	t.Helper()
	if s.backend == nil {
		return
	}
	s.cancel()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	s.server.Close()
	if err := s.gateway.Close(); err != nil {
		t.Error(err)
	}
	if err := s.backend.Stop(stopContext); err != nil {
		t.Error(err)
	}
	s.backend = nil
}

func stopBackend(t *testing.T, backend *tradingservice.Backend) {
	t.Helper()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := backend.Stop(stopContext); err != nil {
		t.Error(err)
	}
}

func openPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
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

func applyCanonicalMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	migrationPath := filepath.Join(
		filepath.Dir(filename), "..", "..", "migrations", "2026082100023.sql",
	)
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply canonical trading migration attempt %d: %v", attempt+1, err)
		}
	}
}

type recoveryProof struct {
	Sequence         int64
	SnapshotSequence int64
	EventHash        string
	SnapshotHash     string
}

func recoveryProofFor(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) recoveryProof {
	t.Helper()
	var proof recoveryProof
	if err := pool.QueryRow(ctx, `
		SELECT market.current_sequence, snapshot.sequence,
		       event.state_hash, snapshot.state_hash
		FROM trading_market AS market
		JOIN trading_snapshot AS snapshot USING (market_id)
		JOIN trading_event_batch AS event
		  ON event.market_id=market.market_id
		 AND event.sequence=market.current_sequence
		WHERE market.market_id=$1
	`, integratedMarketID).Scan(
		&proof.Sequence,
		&proof.SnapshotSequence,
		&proof.EventHash,
		&proof.SnapshotHash,
	); err != nil {
		t.Fatal(err)
	}
	return proof
}

func cleanupMarket(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`DELETE FROM trading_user_session WHERE account_id=$1`,
		"github:qianqiu0404",
	); err != nil {
		t.Error(err)
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
		if _, err := pool.Exec(ctx, `DELETE FROM `+table+` WHERE market_id=$1`, integratedMarketID); err != nil {
			t.Errorf("clean %s: %v", table, err)
		}
	}
}

func doJSON(
	t *testing.T,
	client *http.Client,
	method, endpoint string,
	body any,
	csrf string,
	wantStatus int,
	destination any,
) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", integratedOrigin)
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
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s",
			method, endpoint, response.StatusCode, wantStatus, payload)
	}
	if destination != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, destination); err != nil {
			t.Fatalf("decode %s: %v body=%s", endpoint, err, payload)
		}
	}
}

func cookieValue(
	t *testing.T,
	jar http.CookieJar,
	endpoint, name string,
) string {
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
