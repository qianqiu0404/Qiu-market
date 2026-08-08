package fiatcurrency

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/the-web3/s78-market-services/config"
)

func TestCrawlerContextLivesUntilClose(t *testing.T) {
	crawler, err := NewFiatCurrencyCrawler(
		nil,
		&config.Config{},
		func(error) {},
	)
	require.NoError(t, err)
	require.NoError(t, crawler.resourceCtx.Err())

	require.NoError(t, crawler.Start())
	require.NoError(t, crawler.Close())
	require.ErrorIs(t, crawler.resourceCtx.Err(), context.Canceled)
}
