package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestLegacyFixturesAreBoundedReadResponses(t *testing.T) {
	t.Parallel()
	reads := &atomic.Int64{}
	router := chi.NewRouter()
	mountLegacyReadFixtures(router, reads)
	paths := []string{"/api/v1/get_system_overview", "/api/v1/get_market_insights", "/api/v2/get_asset_dashboard"}
	for _, path := range paths {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`))))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		var body struct {
			Code int `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Code != 2000 {
			t.Fatalf("%s body=%s err=%v", path, response.Body.String(), err)
		}
	}
	if reads.Load() != int64(len(paths)) {
		t.Fatalf("legacy reads=%d", reads.Load())
	}
}

func TestFixtureControlIsStrictAndEvidenceIsGetOnly(t *testing.T) {
	t.Parallel()
	legacyReads := &atomic.Int64{}
	fixture := &fixture{scenario: "success", legacyReads: legacyReads}
	badControl := httptest.NewRecorder()
	fixture.ServeHTTP(badControl, httptest.NewRequest(http.MethodPost, "/__fixture/control", bytes.NewBufferString(`{"scenario":"legacy"}{}`)))
	if badControl.Code != http.StatusBadRequest || fixture.controlWrites.Load() != 0 {
		t.Fatalf("control status=%d writes=%d", badControl.Code, fixture.controlWrites.Load())
	}
	badEvidence := httptest.NewRecorder()
	fixture.ServeHTTP(badEvidence, httptest.NewRequest(http.MethodPost, "/__fixture/evidence", nil))
	if badEvidence.Code != http.StatusMethodNotAllowed || fixture.upstreamNonGET.Load() != 1 {
		t.Fatalf("evidence status=%d nonGET=%d", badEvidence.Code, fixture.upstreamNonGET.Load())
	}
}
