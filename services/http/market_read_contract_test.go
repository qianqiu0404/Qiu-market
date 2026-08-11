package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMarketReadContractBindsReleasePolicyAndRequestNonce(t *testing.T) {
	commit := "a7adc11b142ec0c08d615616ec6d31204d699d83"
	handler := marketReadContractMiddleware(NewMarketReadContract(commit))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/get_market_overview", nil)
	request.Header.Set(publicProxyNonceHeader, "0000000000000000000000000000000a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	for name, want := range map[string]string{
		"X-Qiu-Market-Backend-Release-Commit": commit,
		"X-Qiu-Market-Data-Mode":              MarketReadDataMode,
		"X-Qiu-Market-Provider-Policy":        MarketReadProviderPolicy,
		"X-Qiu-Market-Contract-Schema":        MarketReadContractSchema,
		"X-Qiu-Market-Snapshot-Schema":        MarketReadSnapshotSchema,
		"X-Qiu-Market-Backend-Request-Nonce":  "0000000000000000000000000000000a",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestMarketReadContractNeverClaimsInvalidRelease(t *testing.T) {
	contract := NewMarketReadContract("not-a-release")
	if contract.ReleaseCommit != "" {
		t.Fatalf("invalid release was accepted: %q", contract.ReleaseCommit)
	}
}
