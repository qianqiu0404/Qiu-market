package coinglass

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

func TestOpenInterestRowsSortAndDeduplicateDeterministically(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	firstTime := receivedAt.Add(-2 * time.Hour).UnixMilli()
	lastTime := receivedAt.Add(-time.Hour).UnixMilli()
	first := validOpenInterestRow(firstTime, "6400000000")
	last := validOpenInterestRow(lastTime, "6500000000")

	ordered, err := mapOpenInterest(openInterestHistoryPayload{
		Code: "0", Msg: "success", Data: []openInterestHistoryRow{first, last},
	}, receivedAt)
	require.NoError(t, err)
	reversed, err := mapOpenInterest(openInterestHistoryPayload{
		Code: "0", Msg: "success", Data: []openInterestHistoryRow{last, first, last},
	}, receivedAt)
	require.NoError(t, err)

	orderedEnvelope := ordered.Value.(providercontract.DerivativeSnapshotEnvelope)
	reversedEnvelope := reversed.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.Equal(t, ordered.Meta.Source.SourceID, reversed.Meta.Source.SourceID)
	require.Equal(t, orderedEnvelope.Data, reversedEnvelope.Data)
	require.Contains(t, reversedEnvelope.Meta.Quality, providercontract.QualityOutOfOrder)
	require.Contains(t, reversedEnvelope.Meta.Quality, providercontract.QualityDuplicate)
	require.NotContains(t, orderedEnvelope.Meta.Quality, providercontract.QualityOutOfOrder)
}

func TestLiquidationRowsSortAndDeduplicateDeterministically(t *testing.T) {
	receivedAt := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	first := validLiquidationRow(receivedAt.Add(-2*time.Hour).UnixMilli(), "100")
	last := validLiquidationRow(receivedAt.Add(-time.Hour).UnixMilli(), "200")
	response, err := mapLiquidation(liquidationHistoryPayload{
		Code: "0", Msg: "success", Data: []liquidationHistoryRow{last, first, last},
	}, receivedAt)
	require.NoError(t, err)
	envelope := response.Value.(providercontract.DerivativeSnapshotEnvelope)
	require.Equal(t, "200", envelope.Data.LongLiquidations.Value)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityOutOfOrder)
	require.Contains(t, envelope.Meta.Quality, providercontract.QualityDuplicate)
}

func TestHistoryRowsRejectConflictingDuplicateTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	timestamp := now.Add(-time.Hour).UnixMilli()
	firstOI := validOpenInterestRow(timestamp, "100")
	secondOI := validOpenInterestRow(timestamp, "101")
	_, err := mapOpenInterest(openInterestHistoryPayload{
		Code: "0", Msg: "success", Data: []openInterestHistoryRow{firstOI, secondOI},
	}, now)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorConflict, Provider: ProviderID,
	})

	firstLiquidation := validLiquidationRow(timestamp, "100")
	secondLiquidation := validLiquidationRow(timestamp, "101")
	_, err = mapLiquidation(liquidationHistoryPayload{
		Code: "0", Msg: "success", Data: []liquidationHistoryRow{firstLiquidation, secondLiquidation},
	}, now)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorConflict, Provider: ProviderID,
	})

	precisionA := validOpenInterestRow(timestamp, "100.0")
	precisionB := validOpenInterestRow(timestamp, "100.00")
	_, err = mapOpenInterest(openInterestHistoryPayload{
		Code: "0", Msg: "success", Data: []openInterestHistoryRow{precisionA, precisionB},
	}, now)
	require.ErrorIs(t, err, &providercontract.ProviderError{
		Kind: providercontract.ErrorConflict, Provider: ProviderID,
	})
}

func TestHistoryMappingRejectsFutureAndStaleSourceTime(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	for name, scenario := range map[string]struct {
		eventTime time.Time
		kind      providercontract.ErrorKind
	}{
		"future": {eventTime: now.Add(providercontract.DefaultMaxFutureSkew + time.Millisecond), kind: providercontract.ErrorFuture},
		"stale":  {eventTime: now.Add(-maximumSourceAge - time.Millisecond), kind: providercontract.ErrorStale},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mapOpenInterest(openInterestHistoryPayload{
				Code: "0", Msg: "success",
				Data: []openInterestHistoryRow{validOpenInterestRow(scenario.eventTime.UnixMilli(), "100")},
			}, now)
			require.ErrorIs(t, err, &providercontract.ProviderError{Kind: scenario.kind, Provider: ProviderID})
		})
	}
}

func TestOpenInterestValidatesEveryOHLCFieldAndPrecision(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	for name, scenario := range map[string]struct {
		mutate func(*openInterestHistoryRow)
		kind   providercontract.ErrorKind
	}{
		"missing open": {
			func(row *openInterestHistoryRow) { row.Open = "" }, providercontract.ErrorBadPayload,
		},
		"exponent close": {
			func(row *openInterestHistoryRow) { row.Close = "1e9" }, providercontract.ErrorBadPayload,
		},
		"negative low": {
			func(row *openInterestHistoryRow) { row.Low = "-1" }, providercontract.ErrorBadPayload,
		},
		"inconsistent high": {
			func(row *openInterestHistoryRow) { row.High = "1" }, providercontract.ErrorBadPayload,
		},
		"excess scale": {
			func(row *openInterestHistoryRow) { row.Close = "1.000000000000000000000000000000000000000" }, providercontract.ErrorUnit,
		},
	} {
		t.Run(name, func(t *testing.T) {
			row := validOpenInterestRow(now.Add(-time.Hour).UnixMilli(), "100")
			scenario.mutate(&row)
			_, err := mapOpenInterest(openInterestHistoryPayload{
				Code: "0", Msg: "success", Data: []openInterestHistoryRow{row},
			}, now)
			require.ErrorIs(t, err, &providercontract.ProviderError{Kind: scenario.kind, Provider: ProviderID})
		})
	}
}

func TestOfficialDecimalSchemaRejectsBareJSONNumbers(t *testing.T) {
	for name, scenario := range map[string]struct {
		payload     string
		destination any
	}{
		"open interest": {
			payload:     `{"code":"0","msg":"success","data":[{"time":1786320000000,"open":1,"high":"1","low":"1","close":"1"}]}`,
			destination: &openInterestHistoryPayload{},
		},
		"liquidation": {
			payload:     `{"code":"0","msg":"success","data":[{"time":1786320000000,"long_liquidation_usd":1,"short_liquidation_usd":"1"}]}`,
			destination: &liquidationHistoryPayload{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, json.Unmarshal([]byte(scenario.payload), scenario.destination))
		})
	}
}

func TestLiquidationValidatesSchemaAndNonNegativeUSD(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*liquidationHistoryRow){
		"missing long":   func(row *liquidationHistoryRow) { row.Long = "" },
		"negative short": func(row *liquidationHistoryRow) { row.Short = "-1" },
		"exponent":       func(row *liquidationHistoryRow) { row.Long = "1e3" },
	} {
		t.Run(name, func(t *testing.T) {
			row := validLiquidationRow(now.Add(-time.Hour).UnixMilli(), "100")
			mutate(&row)
			_, err := mapLiquidation(liquidationHistoryPayload{
				Code: "0", Msg: "success", Data: []liquidationHistoryRow{row},
			}, now)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
		})
	}
}

func TestHistoryEnvelopeRequiresSuccessMessageAndData(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	for name, payload := range map[string]openInterestHistoryPayload{
		"provider error code": {Code: "400", Msg: "invalid", Data: []openInterestHistoryRow{validOpenInterestRow(now.UnixMilli(), "1")}},
		"missing message":     {Code: "0", Data: []openInterestHistoryRow{validOpenInterestRow(now.UnixMilli(), "1")}},
		"empty data":          {Code: "0", Msg: "success"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mapOpenInterest(payload, now)
			require.ErrorIs(t, err, &providercontract.ProviderError{
				Kind: providercontract.ErrorBadPayload, Provider: ProviderID,
			})
		})
	}
}

func validOpenInterestRow(timestamp int64, closeValue decimalString) openInterestHistoryRow {
	return openInterestHistoryRow{
		Time: timestamp, Open: closeValue, High: closeValue, Low: closeValue, Close: closeValue,
	}
}

func validLiquidationRow(timestamp int64, longValue decimalString) liquidationHistoryRow {
	return liquidationHistoryRow{Time: timestamp, Long: longValue, Short: "50"}
}
