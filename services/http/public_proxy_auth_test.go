package rest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signPublicProxyRequest(
	request *http.Request,
	secret string,
	now time.Time,
	nonce string,
) {
	body, _ := io.ReadAll(request.Body)
	request.Body = io.NopCloser(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	timestamp := strconv.FormatInt(now.Unix(), 10)
	canonical := publicProxyCanonical(
		timestamp,
		nonce,
		request.Method,
		request.URL.RequestURI(),
		digestHex,
	)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set(publicProxyTimestampHeader, timestamp)
	request.Header.Set(publicProxyNonceHeader, nonce)
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
	signPublicProxyRequest(request, secret, now, "00000000000000000000000000000001")
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
			signPublicProxyRequest(request, secret, time.Now().Add(-time.Minute), "00000000000000000000000000000002")
		}},
		{name: "tampered body", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now(), "00000000000000000000000000000003")
			request.Body = io.NopCloser(strings.NewReader(`{"venue":"uniswap"}`))
		}},
		{name: "tampered path", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now(), "00000000000000000000000000000004")
			request.URL.RawQuery = "source=changed"
		}},
		{name: "malformed nonce", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now(), "00000000000000000000000000000005")
			request.Header.Set(publicProxyNonceHeader, "not-a-canonical-nonce")
		}},
		{name: "tampered nonce", prepare: func(request *http.Request) {
			signPublicProxyRequest(request, secret, time.Now(), "00000000000000000000000000000006")
			request.Header.Set(publicProxyNonceHeader, "00000000000000000000000000000007")
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

func TestPublicProxyHMACRejectsReplayAndNeverCachesFailure(t *testing.T) {
	const secret = "test-only-secret"
	now := time.Now().UTC().Truncate(time.Second)
	requests := make([]*http.Request, 2)
	for index := range requests {
		requests[index] = httptest.NewRequest(
			http.MethodPost,
			"/api/v2/get_market_overview",
			strings.NewReader(`{"venue":"all"}`),
		)
		signPublicProxyRequest(
			requests[index],
			secret,
			now,
			"00000000000000000000000000000008",
		)
	}
	calls := 0
	handler := publicProxyHMACMiddleware(secret)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(http.StatusNoContent)
		},
	))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, requests[0])
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request returned %d", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, requests[1])
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request returned %d", second.Code)
	}
	if got := second.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("replay cache control = %q", got)
	}
	if calls != 1 {
		t.Fatalf("downstream calls = %d, want 1", calls)
	}
}

func TestPublicProxyReplayCacheExpiresAndFailsClosedAtCapacity(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	cache := newPublicProxyReplayCache(1)
	if !cache.accept("00000000000000000000000000000009", now.Add(time.Second), now) {
		t.Fatal("first nonce was rejected")
	}
	if cache.accept("0000000000000000000000000000000a", now.Add(time.Second), now) {
		t.Fatal("cache accepted a new nonce at capacity")
	}
	if cache.accept("00000000000000000000000000000009", now.Add(time.Second), now) {
		t.Fatal("cache accepted a replay")
	}
	if !cache.accept("0000000000000000000000000000000a", now.Add(2*time.Second), now.Add(2*time.Second)) {
		t.Fatal("cache did not release an expired nonce")
	}
}

func TestPublicProxyHMACSetsNoStoreOnDownstreamFailure(t *testing.T) {
	const secret = "test-only-secret"
	handler := publicProxyHMACMiddleware(secret)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/data-quality/summary", nil)
	signPublicProxyRequest(
		request,
		secret,
		time.Now().UTC(),
		"0000000000000000000000000000000b",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("failure cache control = %q", got)
	}
}

func TestPublicProxyCanonicalCrossLanguageFixtures(t *testing.T) {
	type fixture struct {
		Name        string `json:"name"`
		AbsoluteURL string `json:"absoluteURL"`
		Method      string `json:"method"`
		RequestURI  string `json:"requestURI"`
		Body        string `json:"body"`
	}
	raw, err := os.ReadFile("testdata/public_proxy_canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 3 {
		t.Fatalf("fixture count = %d, want 3", len(fixtures))
	}
	const secret = "cross-language-test-secret"
	now := time.Unix(1_800_000_000, 0).UTC()
	for index, current := range fixtures {
		t.Run(current.Name, func(t *testing.T) {
			request := httptest.NewRequest(
				current.Method,
				current.AbsoluteURL,
				strings.NewReader(current.Body),
			)
			if got := request.URL.RequestURI(); got != current.RequestURI {
				t.Fatalf("request URI = %q, want %q", got, current.RequestURI)
			}
			nonce := fmt.Sprintf("%032x", index+16)
			signPublicProxyRequest(request, secret, now, nonce)
			if err := verifyPublicProxyRequest(
				request,
				[]byte(secret),
				now,
				newPublicProxyReplayCache(4),
			); err != nil {
				t.Fatalf("fixture did not verify: %v", err)
			}
		})
	}
}

func TestOperationalPublicProxySignersIncludeNonce(t *testing.T) {
	paths := []string{
		"../../ops/macos/production-lib.sh",
		"../../ops/macos/guardian.sh",
		"../../script/verify-local.sh",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content := string(raw)
			for _, required := range []string{
				"openssl rand -hex 16",
				"X-Qiu-Market-Nonce: $nonce",
				`"$timestamp" "$nonce"`,
			} {
				if !strings.Contains(content, required) {
					t.Fatalf("missing nonce signer contract %q", required)
				}
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
