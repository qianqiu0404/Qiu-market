package crawler

import (
	"context"
	"testing"

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
