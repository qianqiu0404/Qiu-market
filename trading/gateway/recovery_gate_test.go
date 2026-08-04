package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/recovery"
)

func TestRecoveryWriteGuardKeepsReadsAvailableAndRejectsEveryMutation(t *testing.T) {
	coordinator := newTestRecoveryCoordinator(t)
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := recoveryWriteGuard(next, coordinator)

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/trading/orders",
		nil,
	))
	if read.Code != http.StatusNoContent {
		t.Fatalf("read status = %d", read.Code)
	}

	for _, path := range []string{
		"/api/v1/trading/orders",
		"/api/v1/trading/orders/O-1/cancel",
		"/api/v1/trading/admin/fund",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "recovery_in_progress" || body["phase"] != "bootstrap" {
			t.Fatalf("%s body = %v", path, body)
		}
	}
}

func TestRecoveryWriteGuardOpensOnlyAfterWritableProof(t *testing.T) {
	ctx := context.Background()
	coordinator := newTestRecoveryCoordinator(t)
	proof := recovery.Proof{
		RuntimeSequence:    12,
		StateHash:          strings.Repeat("a", 64),
		LedgerBalanced:     true,
		EventContinuous:    true,
		ProjectionCaughtUp: true,
		OutboxCaughtUp:     true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady,
		recovery.PhaseTradingReplay,
		recovery.PhaseReconciling,
		recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		if _, err := coordinator.Advance(ctx, phase, proof); err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
	}
	proof.TransportHealthy = true
	if _, err := coordinator.Advance(ctx, recovery.PhaseWritable, proof); err != nil {
		t.Fatal(err)
	}

	handler := recoveryWriteGuard(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		},
	), coordinator)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/trading/orders",
		strings.NewReader("{}"),
	))
	if response.Code != http.StatusNoContent {
		t.Fatalf("writable request status = %d body=%s", response.Code, response.Body.String())
	}

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/trading/recovery/status",
		nil,
	))
	if statusResponse.Code != http.StatusOK ||
		!strings.Contains(statusResponse.Body.String(), `"writes_enabled":true`) {
		t.Fatalf("recovery status = %d %s", statusResponse.Code, statusResponse.Body.String())
	}
}

func newTestRecoveryCoordinator(t *testing.T) *recovery.Coordinator {
	t.Helper()
	coordinator, err := recovery.NewCoordinator(
		recovery.NewMemoryStore(),
		domain.MarketID("BTC-USDT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	return coordinator
}
