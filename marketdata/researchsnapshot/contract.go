package researchsnapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion   = 1
	UniverseVersion = "core-2026-08-v1"
)

var decimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

type Asset struct {
	ID       string
	Provider string
	Symbol   string
}

var universe = [...]Asset{
	{ID: "BTC-USDT", Provider: "binance", Symbol: "BTCUSDT"},
	{ID: "ETH-USDT", Provider: "binance", Symbol: "ETHUSDT"},
	{ID: "SOL-USDT", Provider: "binance", Symbol: "SOLUSDT"},
	{ID: "SPY"}, {ID: "QQQ"}, {ID: "NVDA"}, {ID: "MSFT"}, {ID: "AAPL"},
	{ID: "TSLA"}, {ID: "COIN"}, {ID: "GLD"},
	{ID: "000300"}, {ID: "000016"}, {ID: "399006"}, {ID: "000688"},
	{ID: "600519"}, {ID: "300750"}, {ID: "002594"}, {ID: "688981"}, {ID: "601318"},
	{ID: "XAU-USD"},
}

func UniverseAssets() []Asset {
	assets := make([]Asset, len(universe))
	copy(assets, universe[:])
	return assets
}

type Quote struct {
	AssetID      string `json:"assetId"`
	Role         string `json:"role"`
	Price        string `json:"price"`
	Currency     string `json:"currency"`
	ObservedAt   string `json:"observedAt"`
	DelaySeconds int64  `json:"delaySeconds"`
	Provider     string `json:"provider"`
	Mode         string `json:"mode"`
	DisplayScope string `json:"displayScope"`
}

type Coverage struct {
	AssetID     string `json:"assetId"`
	Status      string `json:"status"`
	MarketState string `json:"marketState"`
	Reason      string `json:"reason,omitempty"`
}

type Snapshot struct {
	SchemaVersion   int        `json:"schemaVersion"`
	UniverseVersion string     `json:"universeVersion"`
	SnapshotID      string     `json:"snapshotId"`
	AsOf            string     `json:"asOf"`
	GeneratedAt     string     `json:"generatedAt"`
	Mode            string     `json:"mode"`
	Quotes          []Quote    `json:"quotes"`
	Coverage        []Coverage `json:"coverage"`
	Checksum        string     `json:"checksum"`
}

func Finalize(snapshot Snapshot) (Snapshot, error) {
	snapshot.SchemaVersion = SchemaVersion
	snapshot.UniverseVersion = UniverseVersion
	snapshot.SnapshotID = ""
	snapshot.Checksum = ""
	checksum, err := payloadChecksum(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	asOf, err := time.Parse(time.RFC3339Nano, snapshot.AsOf)
	if err != nil {
		return Snapshot{}, fmt.Errorf("invalid asOf: %w", err)
	}
	snapshot.Checksum = checksum
	snapshot.SnapshotID = fmt.Sprintf("market-%s-%s", asOf.UTC().Format("2006-01-02"), checksum[:16])
	if err := Validate(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func Validate(snapshot Snapshot) error {
	if snapshot.SchemaVersion != SchemaVersion || snapshot.UniverseVersion != UniverseVersion {
		return errors.New("unsupported market snapshot contract")
	}
	asOf, err := time.Parse(time.RFC3339Nano, snapshot.AsOf)
	if err != nil {
		return fmt.Errorf("invalid asOf: %w", err)
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, snapshot.GeneratedAt)
	if err != nil {
		return fmt.Errorf("invalid generatedAt: %w", err)
	}
	if generatedAt.Before(asOf.Add(-2*time.Second)) || generatedAt.After(asOf.Add(2*time.Minute)) {
		return errors.New("generatedAt conflicts with asOf")
	}
	if !contains([]string{"live", "delayed", "eod", "mixed"}, snapshot.Mode) {
		return errors.New("invalid snapshot mode")
	}
	if len(snapshot.Coverage) != len(universe) {
		return fmt.Errorf("coverage must contain exactly %d assets", len(universe))
	}
	allowed := make(map[string]struct{}, len(universe))
	for _, asset := range universe {
		allowed[asset.ID] = struct{}{}
	}
	coverageSeen := make(map[string]Coverage, len(universe))
	for _, item := range snapshot.Coverage {
		if _, ok := allowed[item.AssetID]; !ok {
			return fmt.Errorf("unknown coverage asset %q", item.AssetID)
		}
		if _, duplicate := coverageSeen[item.AssetID]; duplicate {
			return fmt.Errorf("duplicate coverage asset %q", item.AssetID)
		}
		coverageSeen[item.AssetID] = item
		if !contains([]string{"healthy", "stale", "unavailable"}, item.Status) {
			return fmt.Errorf("invalid coverage status for %q", item.AssetID)
		}
		if !contains([]string{"open", "closed", "pre", "post", "unknown"}, item.MarketState) {
			return fmt.Errorf("invalid market state for %q", item.AssetID)
		}
		if item.Status == "unavailable" && strings.TrimSpace(item.Reason) == "" {
			return fmt.Errorf("unavailable asset %q requires a reason", item.AssetID)
		}
	}
	quoteSeen := make(map[string]struct{}, len(snapshot.Quotes))
	quotedAssets := make(map[string]struct{}, len(snapshot.Quotes))
	for _, quote := range snapshot.Quotes {
		if _, ok := allowed[quote.AssetID]; !ok {
			return fmt.Errorf("unknown quote asset %q", quote.AssetID)
		}
		key := quote.AssetID + ":" + quote.Role
		if _, duplicate := quoteSeen[key]; duplicate {
			return fmt.Errorf("duplicate quote role for %q", quote.AssetID)
		}
		quoteSeen[key] = struct{}{}
		if !contains([]string{"analysis", "display"}, quote.Role) || !decimalPattern.MatchString(quote.Price) {
			return fmt.Errorf("invalid quote for %q", quote.AssetID)
		}
		if quote.DelaySeconds < 0 || strings.TrimSpace(quote.Currency) == "" || strings.TrimSpace(quote.Provider) == "" {
			return fmt.Errorf("incomplete quote for %q", quote.AssetID)
		}
		observedAt, err := time.Parse(time.RFC3339Nano, quote.ObservedAt)
		if err != nil {
			return fmt.Errorf("invalid observedAt for %q", quote.AssetID)
		}
		expectedDelay := max(int64(0), int64(asOf.Sub(observedAt).Seconds()))
		if observedAt.After(asOf.Add(2*time.Minute)) || absInt64(quote.DelaySeconds-expectedDelay) > 2 {
			return fmt.Errorf("delay conflicts with source time for %q", quote.AssetID)
		}
		coverage := coverageSeen[quote.AssetID]
		if (coverage.Status == "healthy" && quote.DelaySeconds > 300) || (coverage.Status == "stale" && quote.DelaySeconds <= 300) {
			return fmt.Errorf("coverage freshness conflicts with delay for %q", quote.AssetID)
		}
		if !contains([]string{"live", "delayed", "eod"}, quote.Mode) || !contains([]string{"private", "internal_non_display"}, quote.DisplayScope) {
			return fmt.Errorf("invalid quote policy for %q", quote.AssetID)
		}
		if (quote.Role == "display" && quote.DisplayScope != "private") || (quote.Role == "analysis" && quote.DisplayScope != "internal_non_display") {
			return fmt.Errorf("quote role and display scope conflict for %q", quote.AssetID)
		}
		if coverageSeen[quote.AssetID].Status == "unavailable" {
			return fmt.Errorf("unavailable asset %q cannot contain a quote", quote.AssetID)
		}
		quotedAssets[quote.AssetID] = struct{}{}
	}
	for assetID, coverage := range coverageSeen {
		_, hasQuote := quotedAssets[assetID]
		if coverage.Status != "unavailable" && !hasQuote {
			return fmt.Errorf("available asset %q requires a quote", assetID)
		}
	}
	expected, err := payloadChecksum(snapshot)
	if err != nil {
		return err
	}
	if snapshot.Checksum != expected {
		return errors.New("snapshot checksum mismatch")
	}
	expectedID := fmt.Sprintf("market-%s-%s", asOf.UTC().Format("2006-01-02"), expected[:16])
	if snapshot.SnapshotID != expectedID {
		return errors.New("snapshot id mismatch")
	}
	return nil
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func payloadChecksum(snapshot Snapshot) (string, error) {
	snapshot.SnapshotID = ""
	snapshot.Checksum = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", err
	}
	object := value.(map[string]any)
	delete(object, "snapshotId")
	delete(object, "checksum")
	canonical, err := canonicalJSON(object)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := writeCanonicalJSON(&buffer, value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeCanonicalJSON(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil, bool, string, float64:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonicalJSON(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			buffer.Write(encoded)
			buffer.WriteByte(':')
			if err := writeCanonicalJSON(buffer, typed[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", value)
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
