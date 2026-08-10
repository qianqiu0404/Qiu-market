package rest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	publicProxyTimestampHeader = "X-Qiu-Market-Timestamp"
	publicProxyNonceHeader     = "X-Qiu-Market-Nonce"
	publicProxyDigestHeader    = "X-Qiu-Market-Content-SHA256"
	publicProxySignatureHeader = "X-Qiu-Market-Signature"
	publicProxyMaxBodyBytes    = 1 << 20
	publicProxyClockSkew       = 30 * time.Second
	publicProxyMaxReplayNonces = 16_384
)

var publicProxyNoncePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type publicProxyReplayCache struct {
	mu         sync.Mutex
	entries    map[string]time.Time
	maxEntries int
}

func newPublicProxyReplayCache(maxEntries int) *publicProxyReplayCache {
	return &publicProxyReplayCache{
		entries:    make(map[string]time.Time),
		maxEntries: maxEntries,
	}
}

// accept atomically reserves a verified nonce until its signed timestamp can
// no longer pass the clock-skew gate. Capacity exhaustion fails closed rather
// than evicting a still-valid nonce and reopening a replay window.
func (c *publicProxyReplayCache) accept(nonce string, expiresAt, now time.Time) bool {
	if c == nil || c.maxEntries <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for candidate, expiry := range c.entries {
		if expiry.Before(now) {
			delete(c.entries, candidate)
		}
	}
	if expiry, exists := c.entries[nonce]; exists && !expiry.Before(now) {
		return false
	}
	if len(c.entries) >= c.maxEntries {
		return false
	}
	c.entries[nonce] = expiresAt
	return true
}

func publicProxyHMACMiddleware(secret string) func(http.Handler) http.Handler {
	secret = strings.TrimSpace(secret)
	replayCache := newPublicProxyReplayCache(publicProxyMaxReplayNonces)
	return func(next http.Handler) http.Handler {
		if secret == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == HealthPath ||
				r.URL.Path == "/api/v1/trading/events/ws" {
				next.ServeHTTP(w, r)
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			if err := verifyPublicProxyRequest(r, []byte(secret), time.Now().UTC(), replayCache); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"public_proxy_auth_failed","message":"trusted proxy authentication required"}`))
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}

func verifyPublicProxyRequest(
	r *http.Request,
	secret []byte,
	now time.Time,
	replayCache *publicProxyReplayCache,
) error {
	rawTimestamp := strings.TrimSpace(r.Header.Get(publicProxyTimestampHeader))
	timestamp, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid proxy timestamp")
	}
	requestTime := time.Unix(timestamp, 0)
	if requestTime.Before(now.Add(-publicProxyClockSkew)) ||
		requestTime.After(now.Add(publicProxyClockSkew)) {
		return fmt.Errorf("proxy timestamp outside allowed window")
	}
	nonce := strings.TrimSpace(r.Header.Get(publicProxyNonceHeader))
	if !publicProxyNoncePattern.MatchString(nonce) {
		return fmt.Errorf("invalid proxy nonce")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, publicProxyMaxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read proxy body: %w", err)
	}
	if len(body) > publicProxyMaxBodyBytes {
		return fmt.Errorf("proxy body exceeds limit")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	if !hmac.Equal(
		[]byte(strings.ToLower(strings.TrimSpace(r.Header.Get(publicProxyDigestHeader)))),
		[]byte(digestHex),
	) {
		return fmt.Errorf("proxy body digest mismatch")
	}
	canonical := publicProxyCanonical(
		rawTimestamp,
		nonce,
		r.Method,
		r.URL.RequestURI(),
		digestHex,
	)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal(
		[]byte(strings.ToLower(strings.TrimSpace(r.Header.Get(publicProxySignatureHeader)))),
		[]byte(expected),
	) {
		return fmt.Errorf("proxy signature mismatch")
	}
	if !replayCache.accept(nonce, requestTime.Add(publicProxyClockSkew), now) {
		return fmt.Errorf("proxy nonce replayed or replay cache unavailable")
	}
	return nil
}

func publicProxyCanonical(timestamp, nonce, method, requestURI, digest string) string {
	return strings.Join([]string{
		timestamp,
		nonce,
		strings.ToUpper(method),
		requestURI,
		digest,
	}, "\n")
}
