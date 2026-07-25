package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stringPointer(value string) *string {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func TestDecideMarketSnapshotOrderingAndCorrection(t *testing.T) {
	sourceTime := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	observedAt := sourceTime.Add(time.Second)
	current := SymbolMarket{
		Price:        "100000000",
		AskPrice:     "100100000",
		BidPrice:     "99900000",
		Volume:       "500000000",
		Change24hPct: stringPointer("1.25"),
		IsActive:     true,
		SourceTime:   timePointer(sourceTime),
		ObservedAt:   timePointer(observedAt),
	}
	base := MarketSnapshotInput{
		Price:        current.Price,
		AskPrice:     current.AskPrice,
		BidPrice:     current.BidPrice,
		Volume:       current.Volume,
		Change24hPct: stringPointer("1.2500"),
		IsActive:     true,
		SourceTime:   timePointer(sourceTime),
		ObservedAt:   observedAt.Add(time.Second),
	}

	require.Equal(t, MarketSnapshotNoop, decideMarketSnapshot(current, base))

	late := base
	late.SourceTime = timePointer(sourceTime.Add(-time.Millisecond))
	late.Price = "101000000"
	require.Equal(t, MarketSnapshotDiscarded, decideMarketSnapshot(current, late))

	correction := base
	correction.Price = "101000000"
	require.Equal(t, MarketSnapshotCorrection, decideMarketSnapshot(current, correction))

	staleCorrection := correction
	staleCorrection.ObservedAt = observedAt
	require.Equal(t, MarketSnapshotDiscarded, decideMarketSnapshot(current, staleCorrection))

	newerSame := base
	newerSame.SourceTime = timePointer(sourceTime.Add(time.Second))
	require.Equal(t, MarketSnapshotObserved, decideMarketSnapshot(current, newerSame))
}
