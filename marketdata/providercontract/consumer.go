package providercontract

import (
	"errors"
	"reflect"
	"time"
)

// Diagnostic describes a normalized fact's safety or quality without asking
// consumers to parse error strings.
type Diagnostic struct {
	Kind       ErrorKind  `json:"kind"`
	Provider   ProviderID `json:"provider,omitempty"`
	Capability Capability `json:"capability,omitempty"`
	Index      int        `json:"index,omitempty"`
	Field      string     `json:"field,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

// NormalizedEnvelope is the read-only consumer seam. Exactly one concrete
// fact pointer is set. There is deliberately no persistence, cache, network,
// or trading hook on this type.
type NormalizedEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Capability    Capability      `json:"capability"`
	Source        SourceRef       `json:"source"`
	ObservedAt    time.Time       `json:"observed_at"`
	EventTime     *time.Time      `json:"event_time,omitempty"`
	ReceivedAt    time.Time       `json:"received_at"`
	TTL           time.Duration   `json:"ttl"`
	RemainingTTL  time.Duration   `json:"remaining_ttl"`
	Freshness     FreshnessStatus `json:"freshness"`
	Quality       []QualityFlag   `json:"quality,omitempty"`
	Usable        bool            `json:"usable"`
	Diagnostics   []Diagnostic    `json:"diagnostics,omitempty"`

	SpotTicker *SpotTickerEnvelope         `json:"spot_ticker,omitempty"`
	OHLCV      *OHLCVEnvelope              `json:"ohlcv,omitempty"`
	Derivative *DerivativeSnapshotEnvelope `json:"derivative,omitempty"`
	Signal     *SignalEnvelope             `json:"signal,omitempty"`
}

type NormalizedDispatch struct {
	Envelope NormalizedEnvelope `json:"envelope"`
	Trace    DispatchTrace      `json:"trace"`
}

type ConsumerPolicy struct {
	MaxFutureSkew time.Duration
}

// Consumer carries validation policy only. Domain normalization is owned
// exclusively by NormalizeResponse; this type projects its result and adds
// diagnostics.
type Consumer struct {
	policy ConsumerPolicy
}

func NewConsumer(policy ConsumerPolicy) (Consumer, error) {
	if policy.MaxFutureSkew < 0 {
		return Consumer{}, NewError(
			ErrorInvalidTime, "", "new_consumer", nil,
		)
	}
	return Consumer{policy: policy}, nil
}

func NewDefaultConsumer() Consumer {
	return Consumer{policy: ConsumerPolicy{MaxFutureSkew: DefaultMaxFutureSkew}}
}

func (consumer Consumer) Normalize(response Response, now time.Time) (NormalizedEnvelope, error) {
	response, err := NormalizeResponse(response, now, consumer.policy.MaxFutureSkew)
	if err != nil {
		return NormalizedEnvelope{}, err
	}
	freshness, remainingTTL, err := ResponseFreshness(
		response, now, consumer.policy.MaxFutureSkew,
	)
	if err != nil {
		return NormalizedEnvelope{}, err
	}
	meta := response.Meta
	result := NormalizedEnvelope{
		SchemaVersion: meta.SchemaVersion,
		Capability:    response.Capability,
		Source:        meta.Source,
		ObservedAt:    meta.ObservedAt,
		EventTime:     cloneTime(meta.EventTime),
		ReceivedAt:    meta.ReceivedAt,
		TTL:           meta.TTL,
		RemainingTTL:  remainingTTL,
		Freshness:     freshness,
		Quality:       append([]QualityFlag(nil), meta.Quality...),
	}
	result.Diagnostics = consumerDiagnostics(meta, freshness)
	result.Usable = freshness == FreshnessFresh && !hasUnsafeConsumerQuality(meta.Quality)

	switch value := response.Value.(type) {
	case SpotTickerEnvelope:
		cloned := cloneSpotTickerEnvelope(value)
		result.SpotTicker = &cloned
	case *SpotTickerEnvelope:
		cloned := cloneSpotTickerEnvelope(*value)
		result.SpotTicker = &cloned
	case OHLCVEnvelope:
		cloned := cloneOHLCVEnvelope(value)
		result.OHLCV = &cloned
	case *OHLCVEnvelope:
		cloned := cloneOHLCVEnvelope(*value)
		result.OHLCV = &cloned
	case DerivativeSnapshotEnvelope:
		cloned := cloneDerivativeEnvelope(value)
		result.Derivative = &cloned
	case *DerivativeSnapshotEnvelope:
		cloned := cloneDerivativeEnvelope(*value)
		result.Derivative = &cloned
	case SignalEnvelope:
		cloned := cloneSignalEnvelope(value)
		result.Signal = &cloned
	case *SignalEnvelope:
		cloned := cloneSignalEnvelope(*value)
		result.Signal = &cloned
	}
	return result, nil
}

// NormalizeDispatch binds the normalized fact to the router trace that chose
// it. Provider, source, and the final successful attempt must agree.
func (consumer Consumer) NormalizeDispatch(
	result DispatchResult,
	now time.Time,
) (NormalizedDispatch, error) {
	envelope, err := consumer.Normalize(result.Response, now)
	if err != nil {
		return NormalizedDispatch{}, err
	}
	trace := result.Trace
	trace.Attempts = append([]AttemptTrace(nil), result.Trace.Attempts...)
	actual, err := NormalizeProviderID(string(trace.ActualProvider))
	if err != nil {
		return NormalizedDispatch{}, dispatchConflict(envelope, "invalid actual provider")
	}
	source, err := NormalizeSourceRef(trace.Source)
	if err != nil {
		return NormalizedDispatch{}, dispatchConflict(envelope, "invalid trace source")
	}
	trace.ActualProvider = actual
	trace.Source = source
	if actual != envelope.Source.Provider || source != envelope.Source {
		return NormalizedDispatch{}, dispatchConflict(
			envelope, "trace provider or source differs from normalized fact",
		)
	}
	if len(trace.Attempts) == 0 {
		return NormalizedDispatch{}, dispatchConflict(envelope, "trace has no attempts")
	}
	for index := range trace.Attempts {
		attempt := &trace.Attempts[index]
		provider, normalizeErr := NormalizeProviderID(string(attempt.Provider))
		if normalizeErr != nil {
			return NormalizedDispatch{}, dispatchConflict(
				envelope, "trace has invalid attempt provider",
			)
		}
		attempt.Provider = provider
		if attempt.Capability != envelope.Capability || attempt.RetryAfter < 0 {
			return NormalizedDispatch{}, dispatchConflict(
				envelope, "trace attempt capability or retry interval is invalid",
			)
		}
	}
	last := trace.Attempts[len(trace.Attempts)-1]
	if last.ErrorKind != "" || last.Provider != actual || last.CacheHit != trace.CacheHit {
		return NormalizedDispatch{}, dispatchConflict(
			envelope, "final successful attempt does not match selected provider",
		)
	}
	for _, attempt := range trace.Attempts[:len(trace.Attempts)-1] {
		traceError := NewError(attempt.ErrorKind, attempt.Provider, "trace", nil)
		if attempt.CacheHit || !FallbackEligible(traceError) {
			return NormalizedDispatch{}, dispatchConflict(
				envelope, "trace contains a non-fallback-eligible attempt before selection",
			)
		}
	}
	return NormalizedDispatch{Envelope: envelope, Trace: trace}, nil
}

// NormalizeAll preserves input order. Signal identity is scoped by the full
// normalized SourceRef plus EventID: identical records retain one diagnosed
// fact, while conflicting records for that key are all suppressed. Different
// sources are never merged merely because they share a title, asset, or ID.
func (consumer Consumer) NormalizeAll(
	responses []Response,
	now time.Time,
) ([]NormalizedEnvelope, []Diagnostic) {
	values := make([]NormalizedEnvelope, 0, len(responses))
	inputIndexes := make([]int, 0, len(responses))
	diagnostics := make([]Diagnostic, 0)
	for index, response := range responses {
		value, err := consumer.Normalize(response, now)
		if err != nil {
			kind, ok := ErrorKindOf(err)
			if !ok {
				kind = ErrorBadPayload
			}
			diagnostics = append(diagnostics, Diagnostic{
				Kind: kind, Provider: response.Meta.Source.Provider,
				Capability: response.Capability, Index: index, Detail: err.Error(),
			})
			continue
		}
		values = append(values, value)
		inputIndexes = append(inputIndexes, index)
	}

	type signalState struct {
		position   int
		value      SignalEnvelope
		conflicted bool
	}
	seen := make(map[string]signalState)
	suppressed := make(map[int]struct{})
	for position := range values {
		value := &values[position]
		if value.Signal == nil {
			continue
		}
		key := signalDedupKey(value.Source, value.Signal.Data.EventID)
		state, exists := seen[key]
		if !exists {
			seen[key] = signalState{position: position, value: *value.Signal}
			continue
		}
		kind := ErrorDuplicate
		if state.conflicted || !sameSignalFact(state.value, *value.Signal) {
			kind = ErrorConflict
			state.conflicted = true
			suppressed[state.position] = struct{}{}
			suppressed[position] = struct{}{}
		} else {
			suppressed[position] = struct{}{}
			markDuplicate(valueAt(values, state.position))
		}
		seen[key] = state
		diagnostics = append(diagnostics, Diagnostic{
			Kind: kind, Provider: value.Source.Provider,
			Capability: CapabilitySignals, Index: inputIndexes[position],
			Field: "event_id", Detail: value.Signal.Data.EventID,
		})
	}

	result := make([]NormalizedEnvelope, 0, len(values)-len(suppressed))
	for position, value := range values {
		if _, skip := suppressed[position]; !skip {
			result = append(result, value)
		}
	}
	return result, diagnostics
}

func valueAt(values []NormalizedEnvelope, position int) *NormalizedEnvelope {
	return &values[position]
}

func markDuplicate(value *NormalizedEnvelope) {
	value.Quality = addQuality(value.Quality, QualityDuplicate)
	if value.Signal != nil {
		value.Signal.Meta.Quality = append([]QualityFlag(nil), value.Quality...)
	}
	value.Diagnostics = append(value.Diagnostics, Diagnostic{
		Kind: ErrorDuplicate, Provider: value.Source.Provider,
		Capability: value.Capability, Field: "event_id", Detail: value.Signal.Data.EventID,
	})
}

func sameSignalFact(left, right SignalEnvelope) bool {
	return left.Meta.Source == right.Meta.Source &&
		equalOptionalTime(left.Meta.EventTime, right.Meta.EventTime) &&
		reflect.DeepEqual(left.Asset, right.Asset) &&
		reflect.DeepEqual(left.Market, right.Market) &&
		reflect.DeepEqual(left.Data, right.Data)
}

func signalDedupKey(source SourceRef, eventID string) string {
	return string(source.Provider) + "\x00" + source.Key + "\x00" +
		source.SourceID + "\x00" + source.URL + "\x00" + eventID
}

func consumerDiagnostics(meta Metadata, freshness FreshnessStatus) []Diagnostic {
	result := make([]Diagnostic, 0, len(meta.Quality)+1)
	if freshness != FreshnessFresh {
		kind := ErrorStale
		if freshness == FreshnessFuture {
			kind = ErrorFuture
		}
		result = append(result, Diagnostic{
			Kind: kind, Provider: meta.Source.Provider,
			Capability: meta.Capability, Field: "observed_at", Detail: string(freshness),
		})
	}
	for _, quality := range meta.Quality {
		kind := ErrorBadPayload
		switch quality {
		case QualityDuplicate:
			kind = ErrorDuplicate
		case QualityOutOfOrder:
			kind = ErrorOutOfOrder
		case QualityStale:
			kind = ErrorStale
		}
		result = append(result, Diagnostic{
			Kind: kind, Provider: meta.Source.Provider,
			Capability: meta.Capability, Field: "quality", Detail: string(quality),
		})
	}
	return result
}

func hasUnsafeConsumerQuality(values []QualityFlag) bool {
	for _, value := range values {
		if value == QualityMissing || value == QualityStale {
			return true
		}
	}
	return false
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func dispatchConflict(value NormalizedEnvelope, detail string) error {
	return NewError(
		ErrorConflict, value.Source.Provider, "consume_dispatch", errors.New(detail),
	)
}
