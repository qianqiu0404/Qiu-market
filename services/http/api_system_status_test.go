package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/the-web3/s78-market-services/services/http/systemstatus"
)

func TestSystemStatusRouteIsMountedFailClosed(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	mountSystemStatusRoute(router, nil, nil)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, systemstatus.Path, nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want %d", response.Code, http.StatusOK)
	}
	var envelope struct {
		Code   int `json:"code"`
		Result struct {
			SchemaVersion string `json:"schema_version"`
			Overall       struct {
				State string `json:"state"`
			} `json:"overall"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 2000 || envelope.Result.SchemaVersion == "" {
		t.Fatalf("unexpected response: %+v", envelope)
	}
	if envelope.Result.Overall.State == "live" {
		t.Fatalf("missing dependencies must not report live")
	}

	methodResponse := httptest.NewRecorder()
	methodRequest := httptest.NewRequest(http.MethodGet, systemstatus.Path, nil)
	router.ServeHTTP(methodResponse, methodRequest)
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", methodResponse.Code, http.StatusMethodNotAllowed)
	}
}
