package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	cursorSchemaVersion = 1
	cursorDomain        = "qiu-market/trading-cursor/v1"
	cursorMaximumBytes  = 512
	cursorLifetime      = 24 * time.Hour
)

// CursorKeyConfig is populated by the process wiring from private runtime
// configuration. Secrets must never be generated at runtime: doing so would
// invalidate every cursor after a restart.
type CursorKeyConfig struct {
	KeyID  string
	Secret []byte
}

// CursorConfig defines cursor signing and rotation. Current signs and verifies;
// Previous only verifies cursors issued before a key rotation. Now exists for
// deterministic tests and should be nil in production wiring.
type CursorConfig struct {
	Current  CursorKeyConfig
	Previous *CursorKeyConfig
	Now      func() time.Time
}

// ParseCursorConfig decodes the private runtime representation documented by
// PRD-QM-TRADE-001: key_id:base64url-secret. It deliberately rejects missing
// current keys instead of generating an ephemeral secret that would invalidate
// every outstanding cursor after a restart.
func ParseCursorConfig(currentValue, previousValue string) (CursorConfig, error) {
	current, err := parseCursorKeyValue(currentValue)
	if err != nil {
		return CursorConfig{}, fmt.Errorf("current cursor key: %w", err)
	}
	config := CursorConfig{Current: current}
	if previousValue != "" {
		previous, previousErr := parseCursorKeyValue(previousValue)
		if previousErr != nil {
			return CursorConfig{}, fmt.Errorf("previous cursor key: %w", previousErr)
		}
		config.Previous = &previous
	}
	if _, err := newQueryCursorCodec(config); err != nil {
		return CursorConfig{}, err
	}
	return config, nil
}

func parseCursorKeyValue(value string) (CursorKeyConfig, error) {
	keyID, encodedSecret, ok := strings.Cut(value, ":")
	if !ok || keyID == "" || encodedSecret == "" {
		return CursorKeyConfig{}, fmt.Errorf("must use key_id:base64url-secret")
	}
	secret, err := base64.RawURLEncoding.DecodeString(encodedSecret)
	if err != nil {
		return CursorKeyConfig{}, fmt.Errorf("secret must be unpadded base64url")
	}
	key := CursorKeyConfig{KeyID: keyID, Secret: secret}
	if err := validateCursorKey(key); err != nil {
		return CursorKeyConfig{}, err
	}
	return key, nil
}

type queryCursorPayload struct {
	Version       int      `json:"v"`
	KeyID         string   `json:"k"`
	IssuedAt      int64    `json:"i"`
	ExpiresAt     int64    `json:"e"`
	Kind          string   `json:"d"`
	Market        string   `json:"m"`
	AccountDigest string   `json:"a"`
	Filters       string   `json:"f"`
	Sort          string   `json:"s"`
	Position      []string `json:"p"`
}

type queryCursorCodec struct {
	current  CursorKeyConfig
	previous *CursorKeyConfig
	now      func() time.Time
}

func newQueryCursorCodec(config CursorConfig) (*queryCursorCodec, error) {
	if err := validateCursorKey(config.Current); err != nil {
		return nil, fmt.Errorf("current cursor key: %w", err)
	}
	var previous *CursorKeyConfig
	if config.Previous != nil {
		if err := validateCursorKey(*config.Previous); err != nil {
			return nil, fmt.Errorf("previous cursor key: %w", err)
		}
		if config.Previous.KeyID == config.Current.KeyID {
			return nil, fmt.Errorf("current and previous cursor key IDs must differ")
		}
		copyOfPrevious := copyCursorKey(*config.Previous)
		previous = &copyOfPrevious
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &queryCursorCodec{
		current:  copyCursorKey(config.Current),
		previous: previous,
		now:      now,
	}, nil
}

func validateCursorKey(key CursorKeyConfig) error {
	if len(key.KeyID) == 0 || len(key.KeyID) > 32 {
		return fmt.Errorf("key ID must contain 1..32 characters")
	}
	for _, character := range key.KeyID {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' {
			return fmt.Errorf("key ID contains unsupported characters")
		}
	}
	if len(key.Secret) < 32 {
		return fmt.Errorf("secret must contain at least 32 bytes")
	}
	return nil
}

func copyCursorKey(key CursorKeyConfig) CursorKeyConfig {
	return CursorKeyConfig{KeyID: key.KeyID, Secret: append([]byte(nil), key.Secret...)}
}

func (c *queryCursorCodec) Encode(
	kind string,
	market string,
	account string,
	filters string,
	sortOrder string,
	position []string,
) (string, error) {
	if c == nil {
		return "", fmt.Errorf("cursor codec is unavailable")
	}
	now := c.now().UTC().Truncate(time.Second)
	payload := queryCursorPayload{
		Version:       cursorSchemaVersion,
		KeyID:         c.current.KeyID,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(cursorLifetime).Unix(),
		Kind:          kind,
		Market:        market,
		AccountDigest: cursorAccountDigest(c.current.Secret, account),
		Filters:       filters,
		Sort:          sortOrder,
		Position:      append([]string(nil), position...),
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode cursor payload: %w", err)
	}
	payloadToken := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature := cursorSignature(c.current.Secret, payloadToken)
	token := payloadToken + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(token) > cursorMaximumBytes {
		return "", fmt.Errorf("cursor exceeds %d bytes", cursorMaximumBytes)
	}
	return token, nil
}

func (c *queryCursorCodec) Decode(
	token string,
	kind string,
	market string,
	account string,
	filters string,
	sortOrder string,
	positionCount int,
) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("cursor codec is unavailable")
	}
	if len(token) == 0 || len(token) > cursorMaximumBytes || strings.Count(token, ".") != 1 {
		return nil, fmt.Errorf("malformed cursor")
	}
	parts := strings.SplitN(token, ".", 2)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed cursor payload")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return nil, fmt.Errorf("malformed cursor signature")
	}
	var payload queryCursorPayload
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("malformed cursor payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("malformed cursor payload")
	}
	key, ok := c.key(payload.KeyID)
	if !ok {
		return nil, fmt.Errorf("unknown cursor key")
	}
	expectedSignature := cursorSignature(key.Secret, parts[0])
	if subtle.ConstantTimeCompare(signature, expectedSignature) != 1 {
		return nil, fmt.Errorf("invalid cursor signature")
	}
	if payload.Version != cursorSchemaVersion || payload.Kind != kind ||
		payload.Market != market || payload.Filters != filters || payload.Sort != sortOrder ||
		len(payload.Position) != positionCount {
		return nil, fmt.Errorf("cursor does not match this query")
	}
	expectedAccount := cursorAccountDigest(key.Secret, account)
	if subtle.ConstantTimeCompare(
		[]byte(payload.AccountDigest),
		[]byte(expectedAccount),
	) != 1 {
		return nil, fmt.Errorf("cursor does not match this account")
	}
	if payload.ExpiresAt-payload.IssuedAt != int64(cursorLifetime/time.Second) {
		return nil, fmt.Errorf("invalid cursor lifetime")
	}
	now := c.now().UTC().Unix()
	if payload.IssuedAt > now || now >= payload.ExpiresAt {
		return nil, fmt.Errorf("cursor is expired or not yet valid")
	}
	return append([]string(nil), payload.Position...), nil
}

func (c *queryCursorCodec) key(keyID string) (CursorKeyConfig, bool) {
	if keyID == c.current.KeyID {
		return c.current, true
	}
	if c.previous != nil && keyID == c.previous.KeyID {
		return *c.previous, true
	}
	return CursorKeyConfig{}, false
}

func cursorAccountDigest(secret []byte, account string) string {
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte(cursorDomain))
	_, _ = digest.Write([]byte("\x00account\x00"))
	_, _ = digest.Write([]byte(account))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func cursorSignature(secret []byte, payloadToken string) []byte {
	signature := hmac.New(sha256.New, secret)
	_, _ = signature.Write([]byte(cursorDomain))
	_, _ = signature.Write([]byte("\x00token\x00"))
	_, _ = signature.Write([]byte(payloadToken))
	return signature.Sum(nil)
}
