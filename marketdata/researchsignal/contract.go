// Package researchsignal adapts reviewed, read-only research events into a
// canonical wire contract. It cannot submit orders or mutate trading state.
package researchsignal

import (
	"context"
	"fmt"
	"time"
)

const (
	SchemaVersion     = "researchsignals/v1"
	ItemSchemaVersion = "researchsignal/v1"
	Provider          = "xiuqiu-site"
	SourceKind        = "xiuqiu_automated_dynamic"
)

type Status string

const (
	StatusFresh        Status = "fresh"
	StatusEmpty        Status = "empty"
	StatusLegacy       Status = "legacy"
	StatusDegraded     Status = "degraded"
	StatusStale        Status = "stale"
	StatusPartial      Status = "partial"
	StatusUnconfigured Status = "unconfigured"
)

type Freshness string

const (
	FreshnessFresh Freshness = "fresh"
	FreshnessStale Freshness = "stale"
)

type SourceHealth string

const (
	SourceHealthy      SourceHealth = "healthy"
	SourceDegraded     SourceHealth = "degraded"
	SourceUnconfigured SourceHealth = "unconfigured"
)

type ErrorCode string

const (
	ErrorDisabled   ErrorCode = "disabled"
	ErrorBadRequest ErrorCode = "bad_request"
	ErrorNotFound   ErrorCode = "not_found"
	ErrorRateLimit  ErrorCode = "rate_limit"
	ErrorTimeout    ErrorCode = "timeout"
	ErrorNetwork    ErrorCode = "network"
	ErrorUpstream   ErrorCode = "upstream"
	ErrorBadPayload ErrorCode = "bad_payload"
	ErrorConflict   ErrorCode = "conflict"
)

type Error struct {
	Code       ErrorCode
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

type WireError struct {
	Code              ErrorCode `json:"code"`
	Message           string    `json:"message"`
	RetryAfterSeconds *int64    `json:"retryAfterSeconds"`
}

type SourceStatus struct {
	Source        string       `json:"source"`
	Status        SourceHealth `json:"status"`
	LastSuccessAt *string      `json:"lastSuccessAt"`
	Message       *string      `json:"message"`
}

type Summary struct {
	LatestEventAt    *string        `json:"latestEventAt"`
	FreshnessMinutes *int64         `json:"freshnessMinutes"`
	IsDelayed        bool           `json:"isDelayed"`
	EventCount24h    int64          `json:"eventCount24h"`
	P0Count24h       int64          `json:"p0Count24h"`
	P1Count24h       int64          `json:"p1Count24h"`
	Sources          []SourceStatus `json:"sources"`
}

// Signal is intentionally non-executable. Priority is editorial/research
// urgency from the publisher, never an order or risk instruction.
type Signal struct {
	ID            string    `json:"id"`
	SchemaVersion string    `json:"schemaVersion"`
	Type          string    `json:"type"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	Source        string    `json:"source"`
	Provider      string    `json:"provider"`
	SourceURL     string    `json:"sourceUrl"`
	Assets        []string  `json:"assets"`
	EventTime     string    `json:"eventTime"`
	ObservedAt    *string   `json:"observedAt"`
	ReceivedAt    string    `json:"receivedAt"`
	PublishedAt   string    `json:"publishedAt"`
	Freshness     Freshness `json:"freshness"`
	Priority      string    `json:"priority"`
	WatchFor      *string   `json:"watchFor"`
	Invalidation  *string   `json:"invalidation"`
	QualityFlags  []string  `json:"qualityFlags"`
	ContentHash   string    `json:"contentHash"`
	Executable    bool      `json:"executable"`
	SourceKind    string    `json:"sourceKind"`
}

type EventQuery struct {
	Market string
	Asset  string
	Window int
	Limit  int
	Cursor string
}

type EventList struct {
	Items      []Signal `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

type EventDetail struct {
	Item *Signal `json:"item"`
}

type SummaryResult struct {
	Status      Status
	GeneratedAt time.Time
	Data        Summary
	Message     *string
}

type ListResult struct {
	Status      Status
	GeneratedAt time.Time
	Data        EventList
	Message     *string
}

type DetailResult struct {
	Status      Status
	GeneratedAt time.Time
	Data        EventDetail
	Message     *string
}

type Reader interface {
	Summary(context.Context) (SummaryResult, error)
	Events(context.Context, EventQuery) (ListResult, error)
	Event(context.Context, string) (DetailResult, error)
}
