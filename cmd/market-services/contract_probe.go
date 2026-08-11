package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
)

const (
	probeContractSchema = "qiu.market-read-contract.v1"
	probeSnapshotSchema = "qiu.market-snapshot.v1"
	probeEdgeSchema     = "qiu.market-edge-contract.v1"
	probeProviderPolicy = "restricted-no-bypass.v1"
)

var probeReleasePattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var probeSnapshotIDPattern = regexp.MustCompile(`^snp_[0-9a-f]{32}$`)

type contractProbeOptions struct {
	Endpoint        string
	SecretFile      string
	ExpectedRelease string
	ExpectedStatus  int
	RequireEdge     bool
	Client          *http.Client
	Now             func() time.Time
}

func contractProbeCommand() *cli.Command {
	return &cli.Command{
		Name:        "contract-probe",
		Description: "Signed loopback probe for the exact live market-read contract",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "endpoint", Required: true},
			&cli.PathFlag{Name: "secret-file", Required: true},
			&cli.StringFlag{Name: "expected-release", Required: true},
			&cli.IntFlag{Name: "expected-status", Value: http.StatusOK},
			&cli.BoolFlag{Name: "require-edge"},
		},
		Action: func(ctx *cli.Context) error {
			return runContractProbe(ctx.Context, contractProbeOptions{
				Endpoint: ctx.String("endpoint"), SecretFile: ctx.Path("secret-file"),
				ExpectedRelease: ctx.String("expected-release"), ExpectedStatus: ctx.Int("expected-status"),
				RequireEdge: ctx.Bool("require-edge"),
			})
		},
	}
}

func runContractProbe(ctx context.Context, options contractProbeOptions) error {
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" ||
		endpoint.Path != "/api/v2/get_market_overview" || endpoint.RawQuery != "" {
		return fmt.Errorf("contract probe endpoint must be the fixed loopback overview path")
	}
	if !probeReleasePattern.MatchString(options.ExpectedRelease) {
		return fmt.Errorf("contract probe expected release is invalid")
	}
	if options.ExpectedStatus != http.StatusOK && options.ExpectedStatus != http.StatusServiceUnavailable {
		return fmt.Errorf("contract probe expected status must be 200 or 503")
	}
	secret, err := readPrivateProbeSecret(options.SecretFile)
	if err != nil {
		return err
	}
	body := []byte(`{"consumer_token":"live-cutover-probe","venue":"all"}`)
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	timestamp := strconv.FormatInt(now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate contract probe nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	canonical := strings.Join([]string{timestamp, nonce, http.MethodPost, endpoint.RequestURI(), digestHex}, "\n")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create contract probe request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Qiu-Market-Timestamp", timestamp)
	request.Header.Set("X-Qiu-Market-Nonce", nonce)
	request.Header.Set("X-Qiu-Market-Content-SHA256", digestHex)
	request.Header.Set("X-Qiu-Market-Signature", signature)
	client := options.Client
	if client == nil {
		client = &http.Client{
			Timeout:       8 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("execute contract probe: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read contract probe response: %w", err)
	}
	if response.StatusCode != options.ExpectedStatus {
		return fmt.Errorf("contract probe returned HTTP %d", response.StatusCode)
	}
	expectedHeaders := map[string]string{
		"X-Qiu-Market-Backend-Release-Commit": options.ExpectedRelease,
		"X-Qiu-Market-Data-Mode":              "live",
		"X-Qiu-Market-Provider-Policy":        probeProviderPolicy,
		"X-Qiu-Market-Contract-Schema":        probeContractSchema,
		"X-Qiu-Market-Snapshot-Schema":        probeSnapshotSchema,
		"X-Qiu-Market-Backend-Request-Nonce":  nonce,
	}
	if options.RequireEdge {
		expectedHeaders["X-Qiu-Market-Edge-Release-Commit"] = options.ExpectedRelease
		expectedHeaders["X-Qiu-Market-Edge-Data-Mode"] = "live"
		expectedHeaders["X-Qiu-Market-Edge-Contract-Schema"] = probeEdgeSchema
		expectedHeaders["X-Qiu-Data-Mode"] = "live"
	}
	for name, expected := range expectedHeaders {
		if response.Header.Get(name) != expected {
			return fmt.Errorf("contract probe response header %s mismatched", name)
		}
	}
	if options.ExpectedStatus == http.StatusOK {
		var payload struct {
			Code           int    `json:"code"`
			SnapshotID     string `json:"snapshot_id"`
			SnapshotAsOf   int64  `json:"snapshot_as_of"`
			SnapshotSchema string `json:"snapshot_schema"`
			Result         struct {
				AssetCount       int64 `json:"asset_count"`
				FreshAsset       int64 `json:"fresh_asset_count"`
				StaleAsset       int64 `json:"stale_asset_count"`
				UnavailableAsset int64 `json:"unavailable_asset_count"`
			} `json:"result"`
		}
		if err := json.Unmarshal(responseBody, &payload); err != nil || payload.Code != 2000 ||
			!probeSnapshotIDPattern.MatchString(payload.SnapshotID) ||
			payload.SnapshotAsOf <= 0 || payload.SnapshotSchema != probeSnapshotSchema ||
			payload.Result.AssetCount != 106 ||
			payload.Result.AssetCount != payload.Result.FreshAsset+payload.Result.StaleAsset+payload.Result.UnavailableAsset {
			return fmt.Errorf("contract probe response snapshot is invalid")
		}
	} else {
		var payload struct {
			Code string `json:"code"`
		}
		if response.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(responseBody, &payload) != nil ||
			payload.Code != "edge_generation_unavailable" {
			return fmt.Errorf("contract probe drain response is invalid")
		}
	}
	fmt.Printf("market_contract_probe=passed release=%s status=%d edge=%t\n", options.ExpectedRelease, options.ExpectedStatus, options.RequireEdge)
	return nil
}

func readPrivateProbeSecret(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("contract probe secret file is unavailable or unsafe")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Getuid() {
		return nil, fmt.Errorf("contract probe secret file owner is unsafe")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read contract probe secret file: %w", err)
	}
	secret := []byte(strings.TrimSpace(string(raw)))
	if len(secret) < 32 || len(secret) > 4096 {
		return nil, fmt.Errorf("contract probe secret length is invalid")
	}
	return secret, nil
}
