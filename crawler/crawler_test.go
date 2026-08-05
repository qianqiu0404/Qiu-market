package crawler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCrawlerStopAllowsNilOptionalComponents(t *testing.T) {
	cl := &Crawler{}

	require.NoError(t, cl.Stop(context.Background()))
	require.True(t, cl.Stopped())
	require.NoError(t, cl.Stop(context.Background()))
}

func TestBinanceSymbolToPair(t *testing.T) {
	require.Equal(t, "BTC/USDT", binanceSymbolToPair("BTCUSDT"))
	require.Equal(t, "ETH/USDT", binanceSymbolToPair("ETHUSDT"))
	require.Equal(t, "BTC/USDC", binanceSymbolToPair("BTC/USDC"))
}

func TestRepairRangeCompleteRequiresEveryExpectedCandle(t *testing.T) {
	start := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	require.True(t, repairRangeComplete(start, end, time.Minute, []time.Time{
		start.Add(2 * time.Minute),
		start,
		start.Add(time.Minute),
	}))
	require.False(t, repairRangeComplete(start, end, time.Minute, []time.Time{
		start,
		start.Add(2 * time.Minute),
	}))
	require.False(t, repairRangeComplete(start, end, 0, nil))
}
