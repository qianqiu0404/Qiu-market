package rest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signPublicProxyRequest(request *http.Request, secret string, now time.Time) {
	body, _ := io.ReadAll(request.Body)
	request.Body = io.NopCloser(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	timestamp := strconv.FormatInt(now.Unix(), 10)
	canonical := strings.Join([]string{
		timestamp,
		request.Method,
		request.URL.RequestURI(),
		digestHex,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set(publicProxyTimestampHeader, timestamp)
	request.Header.Set(publicProxyDigestHeader, digestHex)
	request.Header.Set(publicProxySignatureHeader, hex.EncodeToString(mac.Sum(nil)))
}

func TestPublicProxyHMACMiddleware(t *testing.T) {
	const secret = "test-only-secret"
	now := time.Now().UTC().Truncate(time.Second)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
		if string(body) != `{"venue":"all"}` {
			t.Errorf("body was not restored: %q", body)
		}
	})
	handler := publicProxyHMACMiddleware(secret)(next)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/get_market_overview?source=test",
		strings.NewReader(`{"venue":"all"}`),
	)
	signPublicProxyRequest(request, secret, now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid signature returned %d: %s", response.Code, response.Body.String())
	}
}

func TestPublicProxyHMACRejectsMissingExpiredAndTamperedRequests(t *testing.T) {
	const secret = "test-only-secret"
	handler := publicProxyHMACMiddleware(secret)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	tests := []struct {
		name    string
		prepare func(*http.Request)
	}{
		{name: "missing", prepare: func(_ *http.Request) {}},
		{name: "expired", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now().Add(-time.Minute))
		}},
		{name: "tampered body", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now())
			request.Body = io.NopCloser(strings.NewReader(`{"venue":"uniswap"}`))
		}},
		{name: "tampered path", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now())
			request.URL.RawQuery = "source=changed"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v2/get_market_overview",
				strings.NewReader(`{"venue":"all"}`),
			)
			test.prepare(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestPublicProxyHMACLeavesHealthAndTicketedWebSocketPublic(t *testing.T) {
	handler := publicProxyHMACMiddleware("test-secret")(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	for _, path := range []string{HealthPath, "/api/v1/trading/events/ws?ticket=opaque"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s returned %d", path, response.Code)
		}
	}
}
