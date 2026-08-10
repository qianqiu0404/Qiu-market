package binancepublic

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

func TestReaderOHLCVFiltersOpenBarAndConvertsInclusiveClose(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	clock := providercontract.NewManualClock(receivedAt)
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads: map[string][]byte{
			OperationKlines: klinesJSON(
				klineRow(receivedAt.Add(-150*time.Second), "60000.12000000"),
				klineRow(receivedAt.Add(-90*time.Second), "60001.12000000"),
				klineRow(receivedAt.Add(-30*time.Second), "60002.12000000"),
			),
		},
	}
	reader, err := newReaderWithTransport(Config{Enabled: true, Clock: clock}, transport)
	require.NoError(t, err)

	result, err := reader.OHLCV(context.Background())
	require.NoError(t, err)
	require.Len(t, transport.calls, 1)
	require.Equal(t, OperationKlines, transport.calls[0].operation)
	require.Equal(t, klinesPath, transport.calls[0].path)
	require.Equal(t, "BTCUSDT", transport.calls[0].query.Get("symbol"))
	require.Equal(t, "1m", transport.calls[0].query.Get("interval"))
	require.Equal(t, "10", transport.calls[0].query.Get("limit"))
	require.Equal(t, "0", transport.calls[0].query.Get("timeZone"))
	require.Len(t, transport.calls[0].query, 4)

	envelope, ok := result.Response.Value.(providercontract.OHLCVEnvelope)
	require.True(t, ok)
	require.Len(t, envelope.Data, 2)
	require.Equal(t, envelope.Data[0].OpenTime.Add(time.Minute), envelope.Data[0].CloseTime)
	require.Equal(t, receivedAt.Add(-90*time.Second), envelope.Data[1].OpenTime)
	require.Equal(t, envelope.Data[1].OpenTime, *envelope.Meta.EventTime)
	require.Equal(t, receivedAt.Add(-30*time.Second), envelope.Meta.ObservedAt)
	require.Equal(t, receivedAt, envelope.Meta.ReceivedAt)
	require.Equal(t, []providercontract.QualityFlag{providercontract.QualityPartial}, envelope.Meta.Quality)
	require.Equal(t, "60000.12", envelope.Data[0].Open.Value)
	require.Equal(t, int32(8), envelope.Data[0].Open.Scale)
	require.Equal(t, providercontract.UnitQuoteAsset, envelope.Data[0].Open.Unit)
	require.Equal(t, providercontract.UnitBaseAsset, envelope.Data[0].Volume.Unit)
}

func TestReaderOHLCVRejectsStaleClosedBarsAndDoesNotCache(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 5, 30, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads: map[string][]byte{
			OperationKlines: klinesJSON(klineRow(receivedAt.Add(-3*time.Minute), "60000")),
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)

	for range 2 {
		_, err = reader.OHLCV(context.Background())
		require.ErrorIs(t, err, &providercontract.ProviderError{
			Kind: providercontract.ErrorStale, Provider: ProviderID,
		})
	}
	require.Len(t, transport.calls, 2)
}

func TestOHLCVRejectsFutureBarInsteadOfHidingItAsPartial(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads: map[string][]byte{OperationKlines: klinesJSON(
			klineRow(receivedAt.Add(-2*time.Minute), "60000"),
			klineRow(receivedAt.Add(3*time.Second), "60001"),
		)},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)
	_, err = reader.OHLCV(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorFuture, Provider: ProviderID,
	})
}

func TestOHLCVSourceIDIsStableForOutOfOrderEquivalentFacts(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	first := klineRow(receivedAt.Add(-150*time.Second), "60000")
	second := klineRow(receivedAt.Add(-90*time.Second), "60001")
	ordered, err := mapOHLCV(decodeKlinesFixture(t, klinesJSON(first, second)), receivedAt)
	require.NoError(t, err)
	reversed, err := mapOHLCV(decodeKlinesFixture(t, klinesJSON(second, first)), receivedAt)
	require.NoError(t, err)
	require.Equal(t, ordered.Meta.Source.SourceID, reversed.Meta.Source.SourceID)
}

func TestReaderOHLCVNormalizesOutOfOrderWithStableSourceID(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 3, 0, 0, time.UTC)
	first := klineRow(receivedAt.Add(-3*time.Minute), "60000")
	second := klineRow(receivedAt.Add(-2*time.Minute), "60001")

	sorted := readKlines(t, receivedAt, first, second)
	reversed := readKlines(t, receivedAt, second, first)
	sortedEnvelope := sorted.Response.Value.(providercontract.OHLCVEnvelope)
	reversedEnvelope := reversed.Response.Value.(providercontract.OHLCVEnvelope)
	require.Equal(t, sorted.Response.Meta.Source.SourceID, reversed.Response.Meta.Source.SourceID)
	require.Equal(t, sortedEnvelope.Data, reversedEnvelope.Data)
	require.True(t, reversedEnvelope.Data[0].OpenTime.Before(reversedEnvelope.Data[1].OpenTime))
	require.Contains(t, reversedEnvelope.Meta.Quality, providercontract.QualityOutOfOrder)
	require.NotContains(t, sortedEnvelope.Meta.Quality, providercontract.QualityOutOfOrder)
}

func TestReaderOHLCVDeduplicatesIdenticalRowsAndRejectsConflicts(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 3, 0, 0, time.UTC)
	row := klineRow(receivedAt.Add(-2*time.Minute), "60000")
	duplicate := readKlines(t, receivedAt, row, append([]any(nil), row...))
	envelope := duplicate.Response.Value.(providercontract.OHLCVEnvelope)
	require.Len(t, envelope.Data, 1)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityDuplicate)

	conflict := append([]any(nil), row...)
	conflict[1] = "60001"
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads:   map[string][]byte{OperationKlines: klinesJSON(row, conflict)},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)
	_, err = reader.OHLCV(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorConflict, Provider: ProviderID,
	})
}

func TestOHLCVValidatesEveryOfficialTupleField(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	for name, mutate := range map[string]func([]any){
		"quote volume type":    func(row []any) { row[7] = 73800 },
		"negative trade count": func(row []any) { row[8] = -1 },
		"negative taker base":  func(row []any) { row[9] = "-0.1" },
		"negative taker quote": func(row []any) { row[10] = "-1" },
		"ignore type":          func(row []any) { row[11] = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			row := klineRow(receivedAt.Add(-2*time.Minute), "60000")
			mutate(row)
			_, err := mapOHLCV(decodeKlinesFixture(t, klinesJSON(row)), receivedAt)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
		})
	}
}

func TestReaderOHLCVFailsClosedWithoutCompletedBar(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads: map[string][]byte{
			OperationKlines: klinesJSON(klineRow(receivedAt.Add(-30*time.Second), "60000")),
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)

	_, err = reader.OHLCV(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
	})
}

func TestReaderOHLCVRejectsInvalidOfficialTupleAndDecimalType(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	valid := klineRow(receivedAt.Add(-2*time.Minute), "60000")
	numericDecimal := append([]any(nil), valid...)
	numericDecimal[1] = 60000

	for name, rows := range map[string][][]any{
		"short tuple":     {valid[:11]},
		"numeric decimal": {numericDecimal},
	} {
		t.Run(name, func(t *testing.T) {
			transport := &scriptedTransport{
				receivedAt: receivedAt,
				payloads:   map[string][]byte{OperationKlines: klinesJSON(rows...)},
			}
			reader, err := newReaderWithTransport(Config{
				Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
			}, transport)
			require.NoError(t, err)
			_, err = reader.OHLCV(context.Background())
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
		})
	}
}

func TestReaderOHLCVRejectsNonMinuteCloseBoundary(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 12, 2, 30, 0, time.UTC)
	row := klineRow(receivedAt.Add(-2*time.Minute), "60000")
	row[6] = receivedAt.Add(-time.Minute).UnixMilli() - 2
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads:   map[string][]byte{OperationKlines: klinesJSON(row)},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)

	_, err = reader.OHLCV(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorConflict, Provider: ProviderID,
	})
}

func TestTickerMappingRejectsWrongSymbolAndExponentDecimal(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*ticker24hPayload){
		"wrong symbol": func(payload *ticker24hPayload) { payload.Symbol = "ETHUSDT" },
		"exponent":     func(payload *ticker24hPayload) { payload.LastPrice = "6e4" },
	} {
		t.Run(name, func(t *testing.T) {
			payload := validTickerPayload(now)
			mutate(&payload)
			_, err := mapTicker(payload, now)
			require.Error(t, err)
			kind, ok := providercontract.ErrorKindOf(err)
			require.True(t, ok)
			require.Contains(t, []providercontract.ErrorKind{
				providercontract.ErrorConflict, providercontract.ErrorBadPayload,
			}, kind)
		})
	}
}

func TestTickerMappingValidatesFullSchemaAndRollingWindow(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*ticker24hPayload){
		"missing weighted average": func(payload *ticker24hPayload) { payload.WeightedAvgPrice = "" },
		"negative bid quantity":    func(payload *ticker24hPayload) { payload.BidQty = "-1" },
		"invalid trade IDs":        func(payload *ticker24hPayload) { payload.FirstID = 200; payload.LastID = 100 },
		"invalid rolling window":   func(payload *ticker24hPayload) { payload.OpenTime = payload.CloseTime + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			payload := validTickerPayload(now)
			mutate(&payload)
			_, err := mapTicker(payload, now)
			require.Error(t, err)
		})
	}
}

func TestReaderTickerRejectsProviderFutureTime(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	transport := &scriptedTransport{
		receivedAt: now,
		payloads: map[string][]byte{
			OperationTicker24h: tickerJSON(now.Add(3 * time.Second)),
		},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(now),
	}, transport)
	require.NoError(t, err)
	_, err = reader.SpotTicker(context.Background())
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorFuture, Provider: ProviderID,
	})
}

func TestDecimalScalePreservesProviderPrecisionWithoutFloat(t *testing.T) {
	value, err := decimalValue("price", "00060000.12000000", providercontract.UnitQuoteAsset)
	require.NoError(t, err)
	require.Equal(t, "00060000.12000000", value.Value)
	require.Equal(t, int32(8), value.Scale)

	_, err = decimalValue("price", "60000.000000000000000000000000000000000000000", providercontract.UnitQuoteAsset)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorUnit, Provider: ProviderID,
	})
}

func validTickerPayload(eventTime time.Time) ticker24hPayload {
	return ticker24hPayload{
		Symbol: providerSymbol, PriceChange: "-759.49", PriceChangePercent: "-1.25",
		WeightedAvgPrice: "60300.12", PrevClosePrice: "60759.61",
		LastPrice: "60000.12", LastQty: "0.01",
		BidPrice: "60000.11", BidQty: "1", AskPrice: "60000.13", AskQty: "2",
		OpenPrice: "60759.61", HighPrice: "61000", LowPrice: "59000",
		Volume: "20", QuoteVolume: "1234567.89",
		OpenTime:  eventTime.Add(-24 * time.Hour).UnixMilli(),
		CloseTime: eventTime.UnixMilli(), FirstID: 100, LastID: 199, Count: 100,
	}
}

func klineRow(openTime time.Time, open string) []any {
	openMS := openTime.UnixMilli()
	return []any{
		openMS,
		open,
		"60010.12000000",
		"59990.12000000",
		"60005.12000000",
		"1.23000000",
		openTime.Add(time.Minute).UnixMilli() - 1,
		"73800.00000000",
		42,
		"0.50000000",
		"30000.00000000",
		"0",
	}
}

func klinesJSON(rows ...[]any) []byte {
	encoded, err := json.Marshal(rows)
	if err != nil {
		panic(fmt.Sprintf("marshal kline fixture: %v", err))
	}
	return encoded
}

func decodeKlinesFixture(t *testing.T, payload []byte) []klinePayload {
	t.Helper()
	var result []klinePayload
	require.NoError(t, json.Unmarshal(payload, &result))
	return result
}

func readKlines(t *testing.T, receivedAt time.Time, rows ...[]any) providercontract.DispatchResult {
	t.Helper()
	transport := &scriptedTransport{
		receivedAt: receivedAt,
		payloads:   map[string][]byte{OperationKlines: klinesJSON(rows...)},
	}
	reader, err := newReaderWithTransport(Config{
		Enabled: true, Clock: providercontract.NewManualClock(receivedAt),
	}, transport)
	require.NoError(t, err)
	result, err := reader.OHLCV(context.Background())
	require.NoError(t, err)
	return result
}
