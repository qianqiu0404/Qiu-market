package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiskWriteGuardFailsClosedOnlyForTradingMutations(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := diskWriteGuard(next, "/path-that-does-not-exist-qiu-market", 1)

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/api/v1/trading/orders", want: http.StatusNoContent},
		{method: http.MethodPost, path: "/api/v1/trading/auth/logout", want: http.StatusNoContent},
		{method: http.MethodPost, path: "/api/v1/trading/ws-ticket", want: http.StatusNoContent},
		{method: http.MethodPost, path: "/api/v1/trading/orders", want: http.StatusServiceUnavailable},
		{
			method: http.MethodPost,
			path:   "/api/v1/trading/orders/order-1/cancel",
			want:   http.StatusServiceUnavailable,
		},
		{method: http.MethodPost, path: "/api/v1/trading/admin/fund", want: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s = %d, want %d", test.method, test.path, response.Code, test.want)
		}
		if test.want == http.StatusServiceUnavailable &&
			!strings.Contains(response.Body.String(), "trading_write_paused") {
			t.Fatalf("pause response = %q", response.Body.String())
		}
	}
}
