package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunContractProbeSignsFromPrivateFileAndValidatesContract(t *testing.T) {
	const (
		secret  = "probe-test-secret-with-at-least-thirty-two-bytes"
		release = "2222222222222222222222222222222222222222"
	)
	secretFile := filepath.Join(t.TempDir(), "probe-secret")
	if err := os.WriteFile(secretFile, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name        string
		status      int
		requireEdge bool
		assetCount  int64
		fresh       int64
		stale       int64
		unavailable int64
		wantError   bool
	}{
		{name: "historical 106 asset slice", status: http.StatusOK, assetCount: 106, fresh: 61, unavailable: 45},
		{name: "expanded 109 asset union", status: http.StatusOK, assetCount: 109, fresh: 61, unavailable: 48},
		{name: "rejects 201 assets", status: http.StatusOK, assetCount: 201, fresh: 61, unavailable: 140, wantError: true},
		{name: "drained edge", status: http.StatusServiceUnavailable, requireEdge: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(body)
				digestHex := hex.EncodeToString(digest[:])
				canonical := strings.Join([]string{
					request.Header.Get("X-Qiu-Market-Timestamp"), request.Header.Get("X-Qiu-Market-Nonce"),
					request.Method, request.URL.RequestURI(), digestHex,
				}, "\n")
				mac := hmac.New(sha256.New, []byte(secret))
				_, _ = mac.Write([]byte(canonical))
				if request.Header.Get("X-Qiu-Market-Content-SHA256") != digestHex ||
					request.Header.Get("X-Qiu-Market-Signature") != hex.EncodeToString(mac.Sum(nil)) {
					http.Error(w, "bad signature", http.StatusUnauthorized)
					return
				}
				nonce := request.Header.Get("X-Qiu-Market-Nonce")
				w.Header().Set("X-Qiu-Market-Backend-Release-Commit", release)
				w.Header().Set("X-Qiu-Market-Data-Mode", "live")
				w.Header().Set("X-Qiu-Market-Provider-Policy", probeProviderPolicy)
				w.Header().Set("X-Qiu-Market-Contract-Schema", probeContractSchema)
				w.Header().Set("X-Qiu-Market-Snapshot-Schema", probeSnapshotSchema)
				w.Header().Set("X-Qiu-Market-Backend-Request-Nonce", nonce)
				if test.requireEdge {
					w.Header().Set("X-Qiu-Market-Edge-Release-Commit", release)
					w.Header().Set("X-Qiu-Market-Edge-Data-Mode", "live")
					w.Header().Set("X-Qiu-Market-Edge-Contract-Schema", probeEdgeSchema)
					w.Header().Set("X-Qiu-Data-Mode", "live")
				}
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(test.status)
				if test.status == http.StatusOK {
					_, _ = fmt.Fprintf(w, `{"code":2000,"snapshot_id":"snp_0123456789abcdef0123456789abcdef","snapshot_as_of":1723000000,"snapshot_schema":"qiu.market-snapshot.v1","result":{"asset_count":%d,"fresh_asset_count":%d,"stale_asset_count":%d,"unavailable_asset_count":%d}}`,
						test.assetCount, test.fresh, test.stale, test.unavailable)
				} else {
					_, _ = fmt.Fprint(w, `{"code":"edge_generation_unavailable"}`)
				}
			})

			err := runContractProbe(t.Context(), contractProbeOptions{
				Endpoint: "http://127.0.0.1:18080/api/v2/get_market_overview", SecretFile: secretFile, ExpectedRelease: release,
				ExpectedStatus: test.status, RequireEdge: test.requireEdge,
				Client: &http.Client{Transport: probeRoundTripper{handler: handler}},
				Now:    func() time.Time { return time.Unix(1_723_000_000, 0) },
			})
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "snapshot is invalid") {
					t.Fatalf("expected invalid snapshot rejection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run contract probe: %v", err)
			}
		})
	}
}

type probeRoundTripper struct {
	handler http.Handler
}

func (transport probeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func TestRunContractProbeRejectsWorldReadableSecret(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "probe-secret")
	if err := os.WriteFile(secretFile, []byte("probe-test-secret-with-at-least-thirty-two-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runContractProbe(t.Context(), contractProbeOptions{
		Endpoint:   "http://127.0.0.1:18080/api/v2/get_market_overview",
		SecretFile: secretFile, ExpectedRelease: strings.Repeat("2", 40), ExpectedStatus: http.StatusOK,
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe secret rejection, got %v", err)
	}
}
