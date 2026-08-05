package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/database"
)

func TestFindKlineGapsGroupsContiguousCandles(t *testing.T) {
	start := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Minute)
	existing := []time.Time{
		start,
		start.Add(3 * time.Minute),
		start.Add(5 * time.Minute),
	}

	gaps := FindKlineGaps(start, end, time.Minute, existing)
	require.Equal(t, []KlineGap{
		{Start: start.Add(time.Minute), End: start.Add(3 * time.Minute)},
		{Start: start.Add(4 * time.Minute), End: start.Add(5 * time.Minute)},
	}, gaps)
}

func TestValidateRepairTask(t *testing.T) {
	task := database.KlineRepairTask{
		SourceSymbol: "BTCUSDT",
		Interval:     "1m",
		GapStart:     time.Now(),
		GapEnd:       time.Now().Add(time.Minute),
	}
	require.NoError(t, validateRepairTask(task))
	task.Interval = "1w"
	require.Error(t, validateRepairTask(task))
}
