package fullstackgolden

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostgresEvidenceSerializesFalseSnapshotTailFacts(t *testing.T) {
	payload, err := json.Marshal(PostgresEvidence{SnapshotSequence: 4, HeadSequence: 7})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshot_matches_head", "snapshot_matches_runtime"} {
		value, present := wire[field]
		if !present || value != false {
			t.Fatalf("%s must preserve false on the audit wire: %s", field, payload)
		}
	}
}

func TestTradingRESTReadWaitsForStableBackendBoundary(t *testing.T) {
	coordinator := &Coordinator{}
	coordinator.gatewaySwitch.Lock()
	called := make(chan struct{})
	handler := coordinator.observeTrading(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(called)
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/trading/balances", nil)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-called:
		t.Fatal("REST read crossed an active backend transition")
	case <-time.After(25 * time.Millisecond):
	}
	coordinator.gatewaySwitch.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("REST read did not resume after backend readiness")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("REST status=%d", response.Code)
	}
}

func TestTradingRESTReadTransitionWaitHonorsContext(t *testing.T) {
	coordinator := &Coordinator{}
	coordinator.gatewaySwitch.Lock()
	t.Cleanup(coordinator.gatewaySwitch.Unlock)
	handler := coordinator.observeTrading(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expired request reached gateway")
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/trading/balances", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("transition timeout status=%d", response.Code)
	}
}
