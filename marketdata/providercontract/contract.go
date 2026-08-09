// Package providercontract defines provider-neutral market-data facts.
//
// The contract intentionally separates provider observation time, market event
// time, and local receipt time. Numeric market values are decimal strings or
// integers; floats are not part of this boundary.
package providercontract

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const SchemaVersion = "providercontract/v1"

type Capability string

const (
	CapabilitySpotTicker  Capability = "spot_ticker"
	CapabilityOHLCV       Capability = "ohlcv"
	CapabilityDerivatives Capability = "derivatives"
	CapabilitySignals     Capability = "signals"
)

type MarketType string

const (
	MarketTypeSpot MarketType = "spot"
	MarketTypePerp MarketType = "perp"
)

type Unit string

const (
	UnitBaseAsset  Unit = "base_asset"
	UnitQuoteAsset Unit = "quote_asset"
	UnitUSD        Unit = "usd"
	UnitContracts  Unit = "contracts"
	UnitCount      Unit = "count"
	UnitRatio      Unit = "ratio"
	UnitPercent    Unit = "percent"
	UnitScore      Unit = "score"
)

// DecimalValue is the only fractional numeric representation admitted at the
// provider boundary. Scale states the maximum accepted fractional precision;
// Value is normalized to a non-exponent decimal string.
type DecimalValue struct {
	Value string `json:"value"`
	Unit  Unit   `json:"unit"`
	Scale int32  `json:"scale"`
}

type QualityFlag string

const (
	QualityDerived     QualityFlag = "derived"
	QualityDuplicate   QualityFlag = "duplicate"
	QualityMissing     QualityFlag = "missing"
	QualityOutOfOrder  QualityFlag = "out_of_order"
	QualityPartial     QualityFlag = "partial"
	QualityProviderGap QualityFlag = "provider_gap"
	QualityStale       QualityFlag = "stale"
)

type ProviderID string

// ProviderIdentity is immutable discovery metadata. DisplayName is cosmetic;
// ID and capabilities are the routing authority.
type ProviderIdentity struct {
	ID           ProviderID   `json:"id"`
	DisplayName  string       `json:"display_name,omitempty"`
	Capabilities []Capability `json:"capabilities"`
}

// SourceRef identifies the exact upstream source. At least one of SourceID or
// URL must be present, so a fact can always be audited without relying on a
// display label.
type SourceRef struct {
	Provider ProviderID `json:"provider"`
	Key      string     `json:"key"`
	SourceID string     `json:"source_id,omitempty"`
	URL      string     `json:"url,omitempty"`
}

type Asset struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol"`
}

// Market keeps the stable repository identity separate from the auditable
// canonical code. Code is always <venue>:<BASE>/<QUOTE>:<spot|perp>.
type Market struct {
	ID    string     `json:"id"`
	Code  string     `json:"code"`
	Venue string     `json:"venue"`
	Base  Asset      `json:"base"`
	Quote Asset      `json:"quote"`
	Type  MarketType `json:"type"`
}

type Metadata struct {
	SchemaVersion string     `json:"schema_version"`
	Source        SourceRef  `json:"source"`
	Capability    Capability `json:"capability"`
	ObservedAt    time.Time  `json:"observed_at"`
	EventTime     *time.Time `json:"event_time,omitempty"`
	ReceivedAt    time.Time  `json:"received_at"`
	// TTL is an internal Go duration. encoding/json represents it as integer
	// nanoseconds; external wire adapters must map it to an explicitly named
	// unit instead of exposing this field as a cross-language contract.
	TTL     time.Duration `json:"ttl"`
	Quality []QualityFlag `json:"quality,omitempty"`
}

type FreshnessStatus string

const (
	FreshnessFresh  FreshnessStatus = "fresh"
	FreshnessStale  FreshnessStatus = "stale"
	FreshnessFuture FreshnessStatus = "future"
)

func (m Metadata) Freshness(now time.Time) FreshnessStatus {
	now = now.UTC()
	if m.ObservedAt.After(now) {
		return FreshnessFuture
	}
	if m.TTL <= 0 || now.Sub(m.ObservedAt) > m.TTL {
		return FreshnessStale
	}
	return FreshnessFresh
}

type SpotTicker struct {
	LastPrice      DecimalValue  `json:"last_price"`
	BidPrice       *DecimalValue `json:"bid_price,omitempty"`
	AskPrice       *DecimalValue `json:"ask_price,omitempty"`
	Open24h        *DecimalValue `json:"open_24h,omitempty"`
	Change24hPct   *DecimalValue `json:"change_24h_pct,omitempty"`
	QuoteTurnover  *DecimalValue `json:"quote_turnover,omitempty"`
	ProviderSymbol string        `json:"provider_symbol"`
}

type SpotTickerEnvelope struct {
	Meta   Metadata   `json:"meta"`
	Market Market     `json:"market"`
	Data   SpotTicker `json:"data"`
}

type OHLCV struct {
	OpenTime  time.Time    `json:"open_time"`
	CloseTime time.Time    `json:"close_time"`
	Open      DecimalValue `json:"open"`
	High      DecimalValue `json:"high"`
	Low       DecimalValue `json:"low"`
	Close     DecimalValue `json:"close"`
	Volume    DecimalValue `json:"volume"`
}

type OHLCVEnvelope struct {
	Meta     Metadata `json:"meta"`
	Market   Market   `json:"market"`
	Interval string   `json:"interval"`
	Data     []OHLCV  `json:"data"`
}

type DerivativeSnapshot struct {
	MarkPrice            *DecimalValue `json:"mark_price,omitempty"`
	IndexPrice           *DecimalValue `json:"index_price,omitempty"`
	FundingRate          *DecimalValue `json:"funding_rate,omitempty"`
	FundingIntervalSec   *int64        `json:"funding_interval_seconds,omitempty"`
	OpenInterest         *DecimalValue `json:"open_interest,omitempty"`
	LongLiquidations     *DecimalValue `json:"long_liquidations,omitempty"`
	ShortLiquidations    *DecimalValue `json:"short_liquidations,omitempty"`
	LiquidationWindowSec *int64        `json:"liquidation_window_seconds,omitempty"`
}

type DerivativeSnapshotEnvelope struct {
	Meta   Metadata           `json:"meta"`
	Market Market             `json:"market"`
	Data   DerivativeSnapshot `json:"data"`
}

type SignalDirection string

const (
	SignalDirectionNegative SignalDirection = "negative"
	SignalDirectionNeutral  SignalDirection = "neutral"
	SignalDirectionPositive SignalDirection = "positive"
	SignalDirectionUnknown  SignalDirection = "unknown"
)

// Signal represents news, on-chain, sentiment, macro, or provider-derived
// events. Value is optional; EventID, Kind, and EventTime are not.
type Signal struct {
	EventID    string          `json:"event_id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Value      *DecimalValue   `json:"value,omitempty"`
	Window     string          `json:"window,omitempty"`
	Direction  SignalDirection `json:"direction"`
	Confidence *DecimalValue   `json:"confidence,omitempty"`
}

type SignalEnvelope struct {
	Meta   Metadata `json:"meta"`
	Asset  *Asset   `json:"asset,omitempty"`
	Market *Market  `json:"market,omitempty"`
	Data   Signal   `json:"data"`
}

// Parameter uses a slice rather than a map so request keys have deterministic
// ordering across adapters and traces.
type Parameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Request struct {
	Capability Capability  `json:"capability"`
	Key        string      `json:"key"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Response struct {
	Capability Capability `json:"capability"`
	Value      any        `json:"value"`
	Meta       Metadata   `json:"meta"`
}

type Provider interface {
	Identity() ProviderIdentity
	Capabilities() []Capability
	Fetch(context.Context, Request) (Response, error)
}

type ErrorKind string

const (
	ErrorAuth            ErrorKind = "auth"
	ErrorPermission      ErrorKind = "permission"
	ErrorUnconfigured    ErrorKind = "unconfigured"
	ErrorBadRequest      ErrorKind = "bad_request"
	ErrorRateLimit       ErrorKind = "rate_limit"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorNetwork         ErrorKind = "network"
	ErrorUpstream5xx     ErrorKind = "upstream_5xx"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorBadPayload      ErrorKind = "bad_payload"
	ErrorInvalidIdentity ErrorKind = "invalid_identity"
	ErrorInvalidSchema   ErrorKind = "invalid_schema"
	ErrorInvalidTime     ErrorKind = "invalid_time"
	ErrorFuture          ErrorKind = "future"
	ErrorStale           ErrorKind = "stale"
	ErrorUnit            ErrorKind = "unit"
	ErrorConflict        ErrorKind = "conflict"
	ErrorDuplicate       ErrorKind = "duplicate"
	ErrorOutOfOrder      ErrorKind = "out_of_order"
	ErrorUnsupported     ErrorKind = "unsupported"
)

type ProviderError struct {
	Kind       ErrorKind
	Provider   ProviderID
	Operation  string
	RetryAfter time.Duration
	Cause      error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := string(e.Kind)
	if e.Provider != "" {
		message = string(e.Provider) + ": " + message
	}
	if e.Operation != "" {
		message += " during " + e.Operation
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ProviderError) Is(target error) bool {
	other, ok := target.(*ProviderError)
	if !ok || e == nil || other == nil {
		return false
	}
	return (other.Kind == "" || e.Kind == other.Kind) &&
		(other.Provider == "" || e.Provider == other.Provider)
}

func (e *ProviderError) Retryable() bool {
	return e != nil && RetryableKind(e.Kind)
}

func NewError(kind ErrorKind, provider ProviderID, operation string, cause error) *ProviderError {
	return &ProviderError{Kind: kind, Provider: provider, Operation: operation, Cause: cause}
}

func NewRetryError(
	kind ErrorKind,
	provider ProviderID,
	operation string,
	retryAfter time.Duration,
	cause error,
) *ProviderError {
	errorValue := NewError(kind, provider, operation, cause)
	if RetryableKind(kind) && retryAfter > 0 {
		errorValue.RetryAfter = retryAfter
	}
	return errorValue
}

func RetryableKind(kind ErrorKind) bool {
	switch kind {
	case ErrorRateLimit, ErrorTimeout, ErrorNetwork, ErrorUpstream5xx, ErrorUnavailable:
		return true
	default:
		return false
	}
}

// FallbackEligible is deliberately false for stale or invalid facts. Routers
// may fall back only for an unsupported capability or a retryable provider
// failure; they must never mask bad data with cache.
func FallbackEligible(err error) bool {
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		return false
	}
	return providerError.Kind == ErrorUnsupported || providerError.Retryable()
}

func ErrorKindOf(err error) (ErrorKind, bool) {
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		return "", false
	}
	return providerError.Kind, true
}

func payloadError(operation, field, reason string) *ProviderError {
	return NewError(ErrorBadPayload, "", operation, fmt.Errorf("%s: %s", field, reason))
}
