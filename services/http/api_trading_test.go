package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestTradingMountReparsesPathAndDoesNotInheritUnaryTimeout(t *testing.T) {
	t.Parallel()
	inner := http.NewServeMux()
	inner.HandleFunc(
		"GET /api/v1/trading/markets/{market}/status",
		func(writer http.ResponseWriter, request *http.Request) {
			select {
			case <-time.After(25 * time.Millisecond):
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(request.PathValue("market")))
			case <-request.Context().Done():
				http.Error(writer, "unexpected timeout", http.StatusGatewayTimeout)
			}
		},
	)

	router := chi.NewRouter()
	router.Group(func(unary chi.Router) {
		unary.Use(func(next http.Handler) http.Handler {
			return http.TimeoutHandler(next, time.Millisecond, "timeout")
		})
		unary.Get("/api/v1/bounded", func(http.ResponseWriter, *http.Request) {})
	})
	mountTradingRoutes(router, inner)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/trading/markets/BTC-USDT/status",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "BTC-USDT" {
		t.Fatalf("mounted trading response = %d/%q", response.Code, response.Body.String())
	}
}
