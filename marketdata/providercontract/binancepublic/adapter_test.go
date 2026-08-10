package binancepublic

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
	s.calls = append(s.calls, transportCall{
		operation: operation, path: path, query: cloneValues(query),
	})
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

func TestReaderDefaultIsDisabledAndCannotReachTransport(t *testing.T) {
	transport := &scriptedTransport{}
	reader, err := newReaderWithTransport(Config{}, transport)
	require.NoError(t, err)

	_, err = reader.SpotTicker(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
	require.Empty(t, transport.calls)

	publicReader, err := NewReader(Config{})
	require.NoError(t, err)
	_, err = publicReader.OHLCV(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnconfigured, Provider: ProviderID,
	})
}

func TestReaderTickerUsesFrozenRequestNormalizesAndCaches(t *testing.T) {
	now := time.Date(2026, 8, 10, 3, 0, 0, 500_000_000, time.UTC)
	clock := providercontract.NewManualClock(now)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationTicker24h: tickerJSON(now.Add(-500 * time.Millisecond)),
		},
	}
	reader, err := newReaderWithTransport(Config{Enabled: true, Clock: clock}, transport)
	require.NoError(t, err)

	first, err := reader.SpotTicker(context.Background())
	require.NoError(t, err)
	second, err := reader.SpotTicker(context.Background())
	require.NoError(t, err)
	require.Len(t, transport.calls, 1)
	require.Equal(t, OperationTicker24h, transport.calls[0].operation)
	require.Equal(t, tickerPath, transport.calls[0].path)
	require.Equal(t, url.Values{
		"symbol": {providerSymbol}, "type": {"FULL"}, "symbolStatus": {"TRADING"},
	}, transport.calls[0].query)
	require.False(t, first.Trace.CacheHit)
	require.True(t, second.Trace.CacheHit)
	require.Equal(t, ProviderID, first.Trace.ActualProvider)
	require.Equal(t, ProviderID, first.Trace.Source.Provider)

	envelope, ok := first.Response.Value.(providercontract.SpotTickerEnvelope)
	require.True(t, ok)
	require.Equal(t, marketID, envelope.Market.ID)
	require.Equal(t, "binance:BTC/USDT:spot", envelope.Market.Code)
	require.Equal(t, "bitcoin", envelope.Market.Base.ID)
	require.Equal(t, "tether", envelope.Market.Quote.ID)
	require.Equal(t, "60000.12", envelope.Data.LastPrice.Value)
	require.Equal(t, int32(8), envelope.Data.LastPrice.Scale)
	require.Equal(t, providercontract.UnitQuoteAsset, envelope.Data.LastPrice.Unit)
	require.Equal(t, "1234567.89", envelope.Data.QuoteTurnover.Value)
	require.Equal(t, int32(8), envelope.Data.QuoteTurnover.Scale)
	require.Equal(t, now.Add(-500*time.Millisecond), envelope.Meta.ObservedAt)
	require.Equal(t, now.Add(-500*time.Millisecond), *envelope.Meta.EventTime)
	require.Equal(t, now, envelope.Meta.ReceivedAt)
}

func TestReaderRejectsRequestMutationBeforeTransport(t *testing.T) {
	transport := &scriptedTransport{}
	reader, err := newReaderWithTransport(Config{Enabled: true}, transport)
	require.NoError(t, err)

	_, err = reader.Fetch(context.Background(), providercontract.Request{
		Capability: providercontract.CapabilitySpotTicker,
		Key:        RequestKey,
		Parameters: []providercontract.Parameter{{Key: "symbol", Value: "ETHUSDT"}},
	})
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadRequest, Provider: ProviderID,
	})
	require.Empty(t, transport.calls)
}

func TestReaderDoesNotCacheFailuresOrStaleTicker(t *testing.T) {
	now := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	clock := providercontract.NewManualClock(now)
	timeout := providercontract.NewError(
		providercontract.ErrorTimeout, ProviderID, "ticker_24h", context.DeadlineExceeded,
	)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationTicker24h: tickerJSON(now.Add(-10 * time.Second)),
		},
		errors: []error{timeout, nil, nil},
	}
	reader, err := newReaderWithTransport(Config{Enabled: true, Clock: clock}, transport)
	require.NoError(t, err)

	_, err = reader.SpotTicker(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorTimeout, Provider: ProviderID,
	})
	_, err = reader.SpotTicker(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorStale, Provider: ProviderID,
	})
	_, err = reader.SpotTicker(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorStale, Provider: ProviderID,
	})
	require.Len(t, transport.calls, 3)
}

func TestReaderConfigIsBounded(t *testing.T) {
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

func TestAdapterIdentityIsStableAndDefensivelyCopied(t *testing.T) {
	provider := &adapter{}
	identity := provider.Identity()
	require.Equal(t, ProviderID, identity.ID)
	require.Equal(t, []providercontract.Capability{
		providercontract.CapabilitySpotTicker,
		providercontract.CapabilityOHLCV,
	}, identity.Capabilities)
	capabilities := provider.Capabilities()
	capabilities[0] = providercontract.CapabilitySignals
	require.False(t, reflect.DeepEqual(capabilities, provider.Capabilities()))
}

func tickerJSON(eventTime time.Time) []byte {
	payload := map[string]any{
		"symbol":             providerSymbol,
		"priceChange":        "-759.49500000",
		"priceChangePercent": "-1.2500",
		"weightedAvgPrice":   "60300.12000000",
		"prevClosePrice":     "60759.61500000",
		"lastPrice":          "60000.12000000",
		"lastQty":            "0.01000000",
		"bidPrice":           "60000.11000000",
		"bidQty":             "1.00000000",
		"askPrice":           "60000.13000000",
		"askQty":             "2.00000000",
		"openPrice":          "60759.61500000",
		"highPrice":          "61000.00000000",
		"lowPrice":           "59000.00000000",
		"volume":             "20.00000000",
		"quoteVolume":        "1234567.89000000",
		"openTime":           eventTime.Add(-24 * time.Hour).UnixMilli(),
		"closeTime":          eventTime.UnixMilli(),
		"firstId":            int64(100),
		"lastId":             int64(199),
		"count":              int64(100),
	}
	encoded, err := json.Marshal(payload)
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
