package rest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	publicProxyTimestampHeader = "X-Qiu-Market-Timestamp"
	publicProxyDigestHeader    = "X-Qiu-Market-Content-SHA256"
	publicProxySignatureHeader = "X-Qiu-Market-Signature"
	publicProxyMaxBodyBytes    = 1 << 20
	publicProxyClockSkew       = 30 * time.Second
)

func publicProxyHMACMiddleware(secret string) func(http.Handler) http.Handler {
	secret = strings.TrimSpace(secret)
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
			if err := verifyPublicProxyRequest(r, []byte(secret), time.Now().UTC()); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"public_proxy_auth_failed","message":"trusted proxy authentication required"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func verifyPublicProxyRequest(r *http.Request, secret []byte, now time.Time) error {
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
	canonical := strings.Join([]string{
		rawTimestamp,
		strings.ToUpper(r.Method),
		r.URL.RequestURI(),
		digestHex,
	}, "\n")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal(
		[]byte(strings.ToLower(strings.TrimSpace(r.Header.Get(publicProxySignatureHeader)))),
		[]byte(expected),
	) {
		return fmt.Errorf("proxy signature mismatch")
	}
	return nil
}
