package researchsnapshot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const IngestPath = "/api/internal/market-snapshots"
const DefaultPreviewIngestURL = "https://xiuqiu-site-m2-preview.vercel.app" + IngestPath

type Publisher struct {
	Client *http.Client
	URL    string
	KeyID  string
	Secret string
	Now    func() time.Time
}

func (publisher Publisher) Publish(ctx context.Context, snapshot Snapshot) error {
	if err := Validate(snapshot); err != nil {
		return err
	}
	parsed, err := url.Parse(strings.TrimSpace(publisher.URL))
	previewTarget := err == nil && parsed.String() == DefaultPreviewIngestURL
	loopbackTarget := err == nil && parsed.Scheme == "http" && isLoopback(parsed.Hostname()) && parsed.Path == IngestPath && parsed.RawQuery == ""
	if !previewTarget && !loopbackTarget {
		return errors.New("publish URL must be the fixed Preview market snapshot ingest endpoint")
	}
	if len(publisher.Secret) < 32 || publisher.KeyID == "" {
		return errors.New("market snapshot publishing credentials are incomplete")
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if publisher.Now != nil {
		now = publisher.Now().UTC()
	}
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	bodyDigest := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(bodyDigest[:])
	canonical := strings.Join([]string{"market-snapshot-v1", publisher.KeyID, http.MethodPost, IngestPath, timestamp, nonce, bodyHash}, "\n")
	signer := hmac.New(sha256.New, []byte(publisher.Secret))
	_, _ = signer.Write([]byte(canonical))
	signature := hex.EncodeToString(signer.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, publisher.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Market-Key-Id", publisher.KeyID)
	req.Header.Set("X-Market-Timestamp", timestamp)
	req.Header.Set("X-Market-Nonce", nonce)
	req.Header.Set("X-Market-Body-Sha256", bodyHash)
	req.Header.Set("X-Market-Signature", signature)
	client := noRedirectClient(publisher.Client, 10*time.Second)
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("publish market snapshot: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("publish market snapshot: HTTP %d", response.StatusCode)
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
