package providercontract

import (
	"context"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConsumerProjectsNormalizedFactAndRemainingTTL(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	meta := consumerTestMetadata(CapabilitySpotTicker, " Binance ", now.Add(-10*time.Second))
	ticker := SpotTickerEnvelope{
		Meta: meta,
		Market: Market{
			ID: "btc-usdt", Venue: " BINANCE ", Type: MarketTypeSpot,
			Base:  Asset{ID: " BITCOIN ", Symbol: " btc "},
			Quote: Asset{ID: " tether ", Symbol: " usdt "},
		},
		Data: SpotTicker{
			LastPrice:      DecimalValue{Value: "60000.12000000", Unit: UnitQuoteAsset, Scale: 8},
			ProviderSymbol: " BTCUSDT ",
		},
	}
	response := Response{Capability: CapabilitySpotTicker, Meta: meta, Value: ticker}

	got, err := NewDefaultConsumer().Normalize(response, now)
	require.NoError(t, err)
	require.True(t, got.Usable)
	require.Equal(t, FreshnessFresh, got.Freshness)
	require.Equal(t, 20*time.Second, got.RemainingTTL)
	require.Equal(t, ProviderID("binance"), got.Source.Provider)
	require.Equal(t, "binance:BTC/USDT:spot", got.SpotTicker.Market.Code)
	require.Equal(t, "60000.12", got.SpotTicker.Data.LastPrice.Value)
	require.Equal(t, int32(8), got.SpotTicker.Data.LastPrice.Scale)
	require.Equal(t, "BTCUSDT", got.SpotTicker.Data.ProviderSymbol)
	require.Nil(t, got.OHLCV)
	require.Nil(t, got.Derivative)
	require.Nil(t, got.Signal)
}

func TestConsumerDelegatesDomainValidationAndOuterInnerEquality(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	meta := consumerTestMetadata(CapabilitySpotTicker, "binance", now)
	ticker := consumerSpot(meta)
	ticker.Data.LastPrice.Value = "6e4"

	_, err := NewDefaultConsumer().Normalize(
		Response{Capability: CapabilitySpotTicker, Meta: meta, Value: ticker}, now,
	)
	requireProviderErrorKind(t, err, ErrorBadPayload)

	ticker = consumerSpot(meta)
	outer := meta
	outer.Source.Provider = "coinbase"
	_, err = NewDefaultConsumer().Normalize(
		Response{Capability: CapabilitySpotTicker, Meta: outer, Value: ticker}, now,
	)
	requireProviderErrorKind(t, err, ErrorConflict)
}

func TestConsumerReturnsStaleFactWithDiagnosticButNotUsable(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	meta := consumerTestMetadata(CapabilitySpotTicker, "binance", now.Add(-time.Minute))
	ticker := consumerSpot(meta)

	got, err := NewDefaultConsumer().Normalize(
		Response{Capability: CapabilitySpotTicker, Meta: meta, Value: ticker}, now,
	)
	require.NoError(t, err)
	require.False(t, got.Usable)
	require.Equal(t, FreshnessStale, got.Freshness)
	require.Zero(t, got.RemainingTTL)
	require.Contains(t, got.Quality, QualityStale)
	require.Contains(t, consumerDiagnosticKinds(got.Diagnostics), ErrorStale)
	require.NotNil(t, got.SpotTicker, "diagnosed stale facts remain inspectable")
}

func TestConsumerIdenticalSignalDuplicateKeepsOneMarkedFact(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	first := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	duplicate := consumerSignalResponse(now.Add(time.Second), " GLASSNODE ", "event-1", "0.80", now.Add(-time.Minute))

	got, diagnostics := NewDefaultConsumer().NormalizeAll([]Response{first, duplicate}, now.Add(time.Second))
	require.Len(t, got, 1)
	require.Len(t, diagnostics, 1)
	require.Equal(t, ErrorDuplicate, diagnostics[0].Kind)
	require.Contains(t, got[0].Quality, QualityDuplicate)
	require.Contains(t, got[0].Signal.Meta.Quality, QualityDuplicate)
	require.True(t, got[0].Usable, "an exact duplicate is safe after deterministic deduplication")
}

func TestConsumerConflictingSameSourceSignalSuppressesAll(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	first := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	conflict := consumerSignalResponse(now, "glassnode", "event-1", "0.9", now.Add(-time.Minute))

	got, diagnostics := NewDefaultConsumer().NormalizeAll([]Response{first, conflict}, now)
	require.Empty(t, got)
	require.Len(t, diagnostics, 1)
	require.Equal(t, ErrorConflict, diagnostics[0].Kind)
}

func TestConsumerNeverDeduplicatesSignalsAcrossProviders(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	glassnode := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	coinglass := consumerSignalResponse(now, "coinglass", "event-1", "0.8", now.Add(-time.Minute))

	got, diagnostics := NewDefaultConsumer().NormalizeAll([]Response{glassnode, coinglass}, now)
	require.Len(t, got, 2)
	require.Empty(t, diagnostics)
	require.Equal(t, ProviderID("glassnode"), got[0].Source.Provider)
	require.Equal(t, ProviderID("coinglass"), got[1].Source.Provider)
}

func TestConsumerNeverDeduplicatesSignalsAcrossDifferentSources(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	reviewed := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	shadow := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	shadow.Meta.Source.Key = "shadow-event"
	shadow.Meta.Source.SourceID = "shadow-1"
	shadowEnvelope := shadow.Value.(SignalEnvelope)
	shadowEnvelope.Meta = shadow.Meta
	shadow.Value = shadowEnvelope

	got, diagnostics := NewDefaultConsumer().NormalizeAll([]Response{reviewed, shadow}, now)
	require.Len(t, got, 2)
	require.Empty(t, diagnostics)
	require.Equal(t, "fixture", got[0].Source.Key)
	require.Equal(t, "shadow-event", got[1].Source.Key)
}

func TestConsumerNormalizeAllPreservesOrderAndReportsInvalidRecords(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	first := consumerSignalResponse(now, "glassnode", "event-1", "0.8", now.Add(-time.Minute))
	invalid := consumerSignalResponse(now, "coinglass", "event-2", "0.7", now.Add(-time.Minute))
	invalidEnvelope := invalid.Value.(SignalEnvelope)
	invalidEnvelope.Data.EventID = ""
	invalid.Value = invalidEnvelope
	last := consumerSignalResponse(now, "santiment", "event-3", "0.6", now.Add(-time.Minute))

	consumer := NewDefaultConsumer()
	got, diagnostics := consumer.NormalizeAll([]Response{first, invalid, last}, now)
	second, secondDiagnostics := consumer.NormalizeAll([]Response{first, invalid, last}, now)
	require.Equal(t, got, second)
	require.Equal(t, diagnostics, secondDiagnostics)
	require.Len(t, got, 2)
	require.Equal(t, ProviderID("glassnode"), got[0].Source.Provider)
	require.Equal(t, ProviderID("santiment"), got[1].Source.Provider)
	require.Len(t, diagnostics, 1)
	require.Equal(t, ErrorBadPayload, diagnostics[0].Kind)
	require.Equal(t, 1, diagnostics[0].Index)
}

func TestConsumerNormalizeDispatchPreservesTimeoutFallbackTrace(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	primary := NewFakeProvider(ProviderIdentity{
		ID: "primary", Capabilities: []Capability{CapabilitySpotTicker},
	})
	secondary := NewFakeProvider(ProviderIdentity{
		ID: "secondary", Capabilities: []Capability{CapabilitySpotTicker},
	})
	require.NoError(t, primary.Script(request, FakeStep{Err: NewError(
		ErrorTimeout, "primary", "fetch", context.DeadlineExceeded,
	)}))
	require.NoError(t, secondary.Script(request, FakeStep{
		Response: consumerSpotResponse(now, "secondary"),
	}))
	router, err := NewRouter(
		[]Provider{primary, secondary}, RouterOptions{Clock: NewManualClock(now)},
	)
	require.NoError(t, err)
	result, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)

	got, err := NewDefaultConsumer().NormalizeDispatch(result, now)
	require.NoError(t, err)
	require.Equal(t, ProviderID("secondary"), got.Envelope.Source.Provider)
	require.Equal(t, ProviderID("secondary"), got.Trace.ActualProvider)
	require.Equal(t, ErrorTimeout, got.Trace.Attempts[0].ErrorKind)
	require.Empty(t, got.Trace.Attempts[1].ErrorKind)

	result.Trace.Attempts[0].Provider = "mutated"
	require.Equal(t, ProviderID("primary"), got.Trace.Attempts[0].Provider)
}

func TestConsumerNormalizeDispatchRejectsProvenanceMismatch(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	response := consumerSpotResponse(now, "secondary")
	validTrace := DispatchTrace{
		Attempts:       []AttemptTrace{{Provider: "secondary", Capability: CapabilitySpotTicker}},
		ActualProvider: "secondary", Source: response.Meta.Source,
	}

	tests := map[string]DispatchTrace{
		"actual provider": func() DispatchTrace {
			value := validTrace
			value.ActualProvider = "primary"
			return value
		}(),
		"source": func() DispatchTrace {
			value := validTrace
			value.Source.SourceID = "relabeled"
			return value
		}(),
		"last successful attempt": func() DispatchTrace {
			value := validTrace
			value.Attempts = []AttemptTrace{{Provider: "primary", Capability: CapabilitySpotTicker}}
			return value
		}(),
		"no attempts": func() DispatchTrace {
			value := validTrace
			value.Attempts = nil
			return value
		}(),
		"auth before success": func() DispatchTrace {
			value := validTrace
			value.Attempts = []AttemptTrace{
				{Provider: "primary", Capability: CapabilitySpotTicker, ErrorKind: ErrorAuth},
				{Provider: "secondary", Capability: CapabilitySpotTicker},
			}
			return value
		}(),
	}
	for name, trace := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := NewDefaultConsumer().NormalizeDispatch(
				DispatchResult{Response: response, Trace: trace}, now,
			)
			requireProviderErrorKind(t, err, ErrorConflict)
		})
	}
}

func TestConsumerOutputDoesNotAliasMutableInputSlices(t *testing.T) {
	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	meta := consumerTestMetadata(CapabilityOHLCV, "binance", now)
	meta.EventTime = consumerTimePointer(now.Add(-time.Minute))
	meta.Quality = []QualityFlag{QualityPartial}
	value := OHLCVEnvelope{
		Meta: meta, Market: consumerMarket("binance", MarketTypeSpot), Interval: "1m",
		Data: []OHLCV{{
			OpenTime: now.Add(-time.Minute), CloseTime: now,
			Open:   consumerDecimal("10", UnitQuoteAsset, 0),
			High:   consumerDecimal("12", UnitQuoteAsset, 0),
			Low:    consumerDecimal("9", UnitQuoteAsset, 0),
			Close:  consumerDecimal("11", UnitQuoteAsset, 0),
			Volume: consumerDecimal("1", UnitBaseAsset, 0),
		}},
	}
	got, err := NewDefaultConsumer().Normalize(
		Response{Capability: CapabilityOHLCV, Meta: meta, Value: value}, now,
	)
	require.NoError(t, err)
	value.Data[0].Close.Value = "999"
	meta.Quality[0] = QualityMissing
	require.Equal(t, "11", got.OHLCV.Data[0].Close.Value)
	require.Equal(t, []QualityFlag{QualityPartial}, got.Quality)
}

func TestConsumerHasNoNetworkEnvironmentPersistenceOrTradingImports(t *testing.T) {
	parsed, err := parser.ParseFile(
		token.NewFileSet(), filepath.Join("consumer.go"), nil, parser.ImportsOnly,
	)
	require.NoError(t, err)
	for _, spec := range parsed.Imports {
		imported := spec.Path.Value
		require.NotContains(t, imported, "net/http")
		require.NotContains(t, imported, `"os"`)
		require.NotContains(t, imported, "/trading/")
		require.NotContains(t, imported, "/database")
		require.NotContains(t, imported, "redis")
	}
}

func consumerTestMetadata(capability Capability, provider ProviderID, observedAt time.Time) Metadata {
	return Metadata{
		SchemaVersion: SchemaVersion,
		Source:        SourceRef{Provider: provider, Key: "fixture", SourceID: "fixture-1"},
		Capability:    capability, ObservedAt: observedAt,
		ReceivedAt: observedAt.Add(time.Second), TTL: 30 * time.Second,
	}
}

func consumerSpot(meta Metadata) SpotTickerEnvelope {
	return SpotTickerEnvelope{
		Meta: meta, Market: consumerMarket(string(meta.Source.Provider), MarketTypeSpot),
		Data: SpotTicker{
			LastPrice: consumerDecimal("60000", UnitQuoteAsset, 0), ProviderSymbol: "BTCUSDT",
		},
	}
}

func consumerSpotResponse(now time.Time, provider ProviderID) Response {
	meta := consumerTestMetadata(CapabilitySpotTicker, provider, now)
	return Response{Capability: CapabilitySpotTicker, Meta: meta, Value: consumerSpot(meta)}
}

func consumerSignalResponse(
	observedAt time.Time,
	provider ProviderID,
	eventID string,
	value string,
	eventTime time.Time,
) Response {
	meta := consumerTestMetadata(CapabilitySignals, provider, observedAt)
	meta.EventTime = consumerTimePointer(eventTime)
	envelope := SignalEnvelope{
		Meta: meta, Asset: &Asset{ID: "bitcoin", Symbol: "BTC"},
		Data: Signal{
			EventID: eventID, Kind: "sentiment.score",
			Value:     &DecimalValue{Value: value, Unit: UnitScore, Scale: 2},
			Direction: SignalDirectionPositive,
		},
	}
	return Response{Capability: CapabilitySignals, Meta: meta, Value: envelope}
}

func consumerMarket(venue string, marketType MarketType) Market {
	return Market{
		ID: "btc-usdt", Venue: venue, Type: marketType,
		Base:  Asset{ID: "bitcoin", Symbol: "BTC"},
		Quote: Asset{ID: "tether", Symbol: "USDT"},
	}
}

func consumerDecimal(value string, unit Unit, scale int32) DecimalValue {
	return DecimalValue{Value: value, Unit: unit, Scale: scale}
}

func consumerTimePointer(value time.Time) *time.Time { return &value }

func consumerDiagnosticKinds(values []Diagnostic) []ErrorKind {
	result := make([]ErrorKind, len(values))
	for index := range values {
		result[index] = values[index].Kind
	}
	return result
}

func requireProviderErrorKind(t *testing.T, err error, expected ErrorKind) {
	t.Helper()
	require.Error(t, err)
	kind, ok := ErrorKindOf(err)
	require.True(t, ok, "expected ProviderError, got %T: %v", err, err)
	require.Equal(t, expected, kind)
}
