package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRepairTaskKeyIsDeterministicAndRangeSensitive(t *testing.T) {
	start := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	first := RepairTaskKey("Binance", "es1", "1m", start, end)
	require.Equal(t, first, RepairTaskKey("binance", "es1", "1m", start, end))
	require.NotEqual(t, first, RepairTaskKey("binance", "es1", "1m", start, end.Add(time.Minute)))
}
