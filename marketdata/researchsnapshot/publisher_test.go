package researchsnapshot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublisherSignsExactBodyWithoutLoggingPrices(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	snapshot, err := Capture(context.Background(), &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: ioNopCloser{strings.NewReader("")}, Header: make(http.Header)}, nil
	})}, now)
	if err != nil {
		t.Fatal(err)
	}
	var received Snapshot
	secret := strings.Repeat("s", 32)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Market-Signature") == "" || request.Header.Get("X-Market-Body-Sha256") == "" {
			t.Fatal("signature headers missing")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodyDigest := sha256.Sum256(body)
		bodyHash := fmt.Sprintf("%x", bodyDigest)
		if request.Header.Get("X-Market-Body-Sha256") != bodyHash {
			t.Fatal("body hash mismatch")
		}
		canonical := strings.Join([]string{
			"market-snapshot-v1", "m2-preview", http.MethodPost, IngestPath,
			request.Header.Get("X-Market-Timestamp"), request.Header.Get("X-Market-Nonce"), bodyHash,
		}, "\n")
		signer := hmac.New(sha256.New, []byte(secret))
		_, _ = signer.Write([]byte(canonical))
		if request.Header.Get("X-Market-Signature") != fmt.Sprintf("%x", signer.Sum(nil)) {
			t.Fatal("HMAC mismatch")
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	publisher := Publisher{Client: server.Client(), URL: server.URL + IngestPath, KeyID: "m2-preview", Secret: secret, Now: func() time.Time { return now }}
	if err := publisher.Publish(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if received.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("published wrong snapshot %s", received.SnapshotID)
	}
}

func TestPublisherRejectsWrongTargetAndWeakSecret(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	snapshot, err := Capture(context.Background(), &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: ioNopCloser{strings.NewReader("")}, Header: make(http.Header)}, nil
	})}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Publisher{URL: "https://example.com/wrong", KeyID: "m2-preview", Secret: strings.Repeat("s", 32)}).Publish(context.Background(), snapshot); err == nil || !strings.Contains(err.Error(), "fixed Preview") {
		t.Fatalf("expected wrong target rejection, got %v", err)
	}
	if err := (Publisher{URL: DefaultPreviewIngestURL, KeyID: "m2-preview", Secret: "short"}).Publish(context.Background(), snapshot); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected weak secret rejection, got %v", err)
	}
}
