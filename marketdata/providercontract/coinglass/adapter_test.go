package coinglass

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

type transportCall struct {
	operation string
	path      string
	query     url.Values
}

type scriptedTransport struct {
	receivedAt time.Time
	payloads   map[string][]byte
	errors     []error
	calls      []transportCall
}

func (s *scriptedTransport) DoJSON(
	_ context.Context,
	operation string,
	path string,
	query url.Values,
	dst any,
) (time.Time, error) {
	s.calls = append(s.calls, transportCall{operation: operation, path: path, query: cloneValues(query)})
	if len(s.errors) > 0 {
		err := s.errors[0]
		s.errors = s.errors[1:]
		if err != nil {
			return time.Time{}, err
		}
	}
	payload, ok := s.payloads[operation]
	if !ok {
		return time.Time{}, errors.New("fixture payload is missing")
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return time.Time{}, err
	}
	return s.receivedAt, nil
}

func TestReaderDefaultDisabledAndFundingFailsBeforeNetwork(t *testing.T) {
	transport := &scriptedTransport{}
	reader, err := newReaderWithTransport(Config{}, transport)
	require.NoError(t, err)

	_, err = reader.OpenInterest(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
	_, err = reader.Liquidation(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
	_, err = reader.FundingRate(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnsupported, Provider: ProviderID,
	})
	require.Empty(t, transport.calls)

	publicReader, err := NewReader(Config{})
	require.NoError(t, err)
	_, err = publicReader.OpenInterest(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
}

func TestNewReaderEnabledRequiresSecretProviderBeforeHTTPConstruction(t *testing.T) {
	_, err := NewReader(Config{Enabled: true})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
}

func TestReaderUsesFrozenOpenInterestRequestAndCaches(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationOpenInterestHistory: openInterestJSON(now.Add(-time.Hour)),
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(now),
	}, transport)
	require.NoError(t, err)

	first, err := reader.OpenInterest(context.Background())
	require.NoError(t, err)
	second, err := reader.OpenInterest(context.Background())
	require.NoError(t, err)
	require.Len(t, transport.calls, 1)
	require.Equal(t, OperationOpenInterestHistory, transport.calls[0].operation)
	require.Equal(t, openInterestHistoryPath, transport.calls[0].path)
	require.Equal(t, url.Values{
		"exchange": {providerExchange}, "symbol": {providerInstrument},
		"interval": {historyInterval}, "limit": {historyLimit}, "unit": {"usd"},
	}, transport.calls[0].query)
	require.False(t, first.Trace.CacheHit)
	require.True(t, second.Trace.CacheHit)
	require.Equal(t, ProviderID, first.Trace.ActualProvider)

	envelope := first.Response.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.Equal(t, marketID, envelope.Market.ID)
	require.Equal(t, "binance:BTC/USD:perp", envelope.Market.Code)
	require.Equal(t, "USD", envelope.Market.Quote.Symbol)
	require.Equal(t, "6500000000.125", envelope.Data.OpenInterest.Value)
	require.Equal(t, providercontract.UnitUSD, envelope.Data.OpenInterest.Unit)
	require.Equal(t, int32(3), envelope.Data.OpenInterest.Scale)
	require.Nil(t, envelope.Data.FundingRate)
	require.Equal(t, now, envelope.Meta.ObservedAt)
	require.Equal(t, now.Add(-time.Hour), *envelope.Meta.EventTime)
	require.Equal(t, now, envelope.Meta.ReceivedAt)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityDerived)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityPartial)
	require.Contains(t, envelope.Meta.Source.SourceID, "instrument=BTCUSD_PERP")
	require.Contains(t, envelope.Meta.Source.SourceID, "settlement=USDT")
}

func TestReaderUsesFrozenLiquidationRequest(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationLiquidationHistory: liquidationJSON(now.Add(-time.Hour)),
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(now),
	}, transport)
	require.NoError(t, err)
	result, err := reader.Liquidation(context.Background())
	require.NoError(t, err)
	require.Len(t, transport.calls, 1)
	require.Equal(t, liquidationHistoryPath, transport.calls[0].path)
	require.Equal(t, url.Values{
		"exchange": {providerExchange}, "symbol": {providerInstrument},
		"interval": {historyInterval}, "limit": {historyLimit},
	}, transport.calls[0].query)
	envelope := result.Response.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.Equal(t, "1250000.25", envelope.Data.LongLiquidations.Value)
	require.Equal(t, "975000.125", envelope.Data.ShortLiquidations.Value)
	require.Equal(t, int64(14400), *envelope.Data.LiquidationWindowSec)
	require.Nil(t, envelope.Data.OpenInterest)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityDerived)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityPartial)
}

func TestReaderRejectsRequestMutationBeforeTransport(t *testing.T) {
	transport := &scriptedTransport{}
	reader, err := newReaderWithTransport(Config{Enabled: true}, transport)
	require.NoError(t, err)

	_, err = reader.Fetch(context.Background(), providercontract.Request{
		Capability: providercontract.CapabilityDerivatives,
		Key:        RequestKeyOpenInterest,
		Parameters: []providercontract.Parameter{{Key: "symbol", Value: "ETHUSD_PERP"}},
	})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
	})
	_, err = reader.Fetch(context.Background(), derivativesRequest("BTC-USD-PERP:unknown"))
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
	})
	require.Empty(t, transport.calls)
}

func TestReaderDoesNotCacheTransportOrStaleFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationOpenInterestHistory: openInterestJSON(now.Add(-maximumSourceAge - time.Second)),
		},
		errors: []error{
			providercontract.NewError(providercontract.ErrorTimeout, ProviderID, "open_interest_history", context.DeadlineExceeded),
			nil,
			nil,
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(now),
	}, transport)
	require.NoError(t, err)

	_, err = reader.OpenInterest(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorTimeout, Provider: ProviderID,
	})
	for range 2 {
		_, err = reader.OpenInterest(context.Background())
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorStale, Provider: ProviderID,
		})
	}
	require.Len(t, transport.calls, 3)
}

func TestDiscoveryIsExactAndDefensivelyAllocated(t *testing.T) {
	discovery := (&Reader{}).Discovery()
	require.Equal(t, ProviderID, discovery.Provider.ID)
	require.Equal(t, contractIdentity, discovery.Contract)
	require.Equal(t, "BTCUSD_PERP", discovery.Contract.InstrumentID)
	require.Equal(t, "USD", discovery.Contract.QuoteAsset)
	require.Equal(t, "USDT", discovery.Contract.SettlementCurrency)
	require.Equal(t, []MetricSupport{
		{Metric: MetricOpenInterest, Supported: true, Unit: providercontract.UnitUSD, EndpointPath: openInterestHistoryPath},
		{Metric: MetricFundingRate, Supported: false, Reason: fundingUnsupported},
		{Metric: MetricLiquidation, Supported: true, Unit: providercontract.UnitUSD, WindowSec: liquidationWindow, EndpointPath: liquidationHistoryPath},
	}, discovery.Metrics)
	discovery.Metrics[0].Supported = false
	discovery.Provider.Capabilities[0] = providercontract.CapabilitySignals
	require.True(t, (&Reader{}).Discovery().Metrics[0].Supported)
	require.Equal(t, providercontract.CapabilityDerivatives, (&Reader{}).Discovery().Provider.Capabilities[0])

	provider := &adapter{}
	capabilities := provider.Capabilities()
	capabilities[0] = providercontract.CapabilitySignals
	require.False(t, reflect.DeepEqual(capabilities, provider.Capabilities()))
}

func TestReaderConfigBounds(t *testing.T) {
	_, err := NewReader(Config{CacheCapacity: MaximumCacheCapacity + 1})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
	})
	_, err = NewReader(Config{CacheTTL: MaximumCacheTTL + time.Nanosecond})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
	})
	config, err := normalizeConfig(Config{})
	require.NoError(t, err)
	require.Equal(t, DefaultCacheCapacity, config.CacheCapacity)
	require.Equal(t, DefaultCacheTTL, config.CacheTTL)
}

func openInterestJSON(eventTime time.Time) []byte {
	return mustJSON(map[string]any{
		"code": "0", "msg": "success",
		"data": []map[string]any{{
			"time": eventTime.UnixMilli(), "open": "6400000000.125",
			"high": "6600000000.125", "low": "6300000000.125", "close": "6500000000.125",
		}},
	})
}

func liquidationJSON(eventTime time.Time) []byte {
	return mustJSON(map[string]any{
		"code": "0", "msg": "success",
		"data": []map[string]any{{
			"time":                  eventTime.UnixMilli(),
			"long_liquidation_usd":  "1250000.25",
			"short_liquidation_usd": "975000.125",
		}},
	})
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func cloneValues(input url.Values) url.Values {
	result := make(url.Values, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}
