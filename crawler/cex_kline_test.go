package crawler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/database"
)

func TestNormalizeRawKlineRowsSortsAndFiltersProviderRows(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC().Truncate(time.Minute)
	end := start.Add(3 * time.Minute)
	raw := func(values ...string) []json.RawMessage {
		result := make([]json.RawMessage, 0, len(values))
		for _, value := range values {
			result = append(result, json.RawMessage(value))
		}
		return result
	}
	rows, err := normalizeRawKlineRows(
		[][]json.RawMessage{
			raw(
				`"`+formatUnixMillis(start.Add(time.Minute))+`"`,
				`"2"`, `"3"`, `"1"`, `"2.5"`, `"12"`,
			),
			raw(
				formatUnixMillis(start),
				`"1"`, `"2"`, `"0.5"`, `"1.5"`, `"10"`,
			),
		},
		klineRawLayout{
			TimeIndex: 0, OpenIndex: 1, HighIndex: 2, LowIndex: 3,
			CloseIndex: 4, VolumeIndex: 5, TimestampUnit: time.Millisecond,
		},
		start, end,
	)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, start, rows[0].OpenTime)
	require.Equal(t, "2.5", rows[1].Close)
}

func TestAggregateOneMinuteBucketBuildsDeterministicOHLCV(t *testing.T) {
	start := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	rows := make([]database.SymbolKline, 0, 15)
	for index := 0; index < 15; index++ {
		rows = append(rows, database.SymbolKline{
			OpenTime:   start.Add(time.Duration(index) * time.Minute),
			OpenPrice:  formatInteger(100 + index),
			HighPrice:  formatInteger(110 + index),
			LowPrice:   formatInteger(90 - index),
			ClosePrice: formatInteger(105 + index),
			Volume:     "2",
		})
	}
	market := database.ProviderMarket{
		MarketID: "market-1", SymbolGuid: "symbol-1",
	}
	result, complete, err := aggregateOneMinuteBucket(
		market, "15m", start, rows, start.Add(15*time.Minute),
	)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, "100", result.OpenPrice)
	require.Equal(t, "124", result.HighPrice)
	require.Equal(t, "76", result.LowPrice)
	require.Equal(t, "119", result.ClosePrice)
	require.Equal(t, "30", result.Volume)
	require.Equal(t, "15m", result.Interval)
	require.Equal(t, start, result.OpenTime)
}

func TestAggregateOneMinuteBucketAcceptsPostgresNumericScale(t *testing.T) {
	start := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	rows := make([]database.SymbolKline, 0, 15)
	for index := 0; index < 15; index++ {
		rows = append(rows, database.SymbolKline{
			OpenTime:   start.Add(time.Duration(index) * time.Minute),
			OpenPrice:  fmt.Sprintf("%d.000000000000000000", 100+index),
			HighPrice:  fmt.Sprintf("%d.000000000000000000", 110+index),
			LowPrice:   fmt.Sprintf("%d.000000000000000000", 90-index),
			ClosePrice: fmt.Sprintf("%d.000000000000000000", 105+index),
			Volume:     "2.000000000000000000",
		})
	}
	result, complete, err := aggregateOneMinuteBucket(
		database.ProviderMarket{MarketID: "market-1", SymbolGuid: "symbol-1"},
		"15m", start, rows, start.Add(15*time.Minute),
	)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, "100", result.OpenPrice)
	require.Equal(t, "124", result.HighPrice)
	require.Equal(t, "76", result.LowPrice)
	require.Equal(t, "119", result.ClosePrice)
	require.Equal(t, "30", result.Volume)
}

func TestScaledIntegerRejectsFractionalScaledValue(t *testing.T) {
	_, err := scaledInteger("12.000000000000000001")
	require.ErrorContains(t, err, "fractional component")
}

func TestPreferredProviderHTTPStatusPrioritizesRestricted(t *testing.T) {
	require.Equal(t, http.StatusForbidden, preferredProviderHTTPStatus(
		0, &providerHTTPError{host: "provider.invalid", status: http.StatusForbidden},
	))
	require.Equal(t, http.StatusUnavailableForLegalReasons, preferredProviderHTTPStatus(
		http.StatusBadGateway,
		&providerHTTPError{host: "provider.invalid", status: http.StatusUnavailableForLegalReasons},
	))
	require.Equal(t, http.StatusBadGateway, preferredProviderHTTPStatus(
		http.StatusBadGateway, fmt.Errorf("later decode failure"),
	))
}

func TestAggregateOneMinuteBucketRejectsHistoricalGap(t *testing.T) {
	start := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	rows := []database.SymbolKline{
		{
			OpenTime: start, OpenPrice: "100", HighPrice: "110",
			LowPrice: "90", ClosePrice: "105", Volume: "2",
		},
		{
			OpenTime: start.Add(2 * time.Minute), OpenPrice: "105",
			HighPrice: "115", LowPrice: "95", ClosePrice: "110", Volume: "3",
		},
	}
	_, complete, err := aggregateOneMinuteBucket(
		database.ProviderMarket{MarketID: "market-1", SymbolGuid: "symbol-1"},
		"15m", start, rows, start.Add(20*time.Minute),
	)
	require.NoError(t, err)
	require.False(t, complete)
}

func formatUnixMillis(value time.Time) string {
	return formatInteger(int(value.UnixMilli()))
}

func formatInteger(value int) string {
	return fmt.Sprintf("%d", value)
}
