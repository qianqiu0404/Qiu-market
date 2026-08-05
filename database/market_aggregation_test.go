package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDashboardVenueRejectsUnknownValues(t *testing.T) {
	provider, kind, err := NormalizeDashboardVenue("coinbase")
	require.NoError(t, err)
	require.Equal(t, "coinbase", provider)
	require.Equal(t, "venue_spot", kind)

	_, _, err = NormalizeDashboardVenue("not-a-provider")
	require.ErrorIs(t, err, ErrInvalidDashboardVenue)
	require.True(t, errors.Is(err, ErrInvalidDashboardVenue))
}

func TestNormalizeSelectionProviderSupportsCEXAndDEX(t *testing.T) {
	expected := []string{
		"binance", "coinbase", "bybit", "okx",
		"hyperliquid", "uniswap", "pancakeswap",
	}
	require.Equal(t, expected, providerSelectionUniverse())
	for _, provider := range expected {
		normalized, err := normalizeSelectionProvider(provider)
		require.NoError(t, err)
		require.Equal(t, provider, normalized)
	}

	_, err := normalizeSelectionProvider("unknown")
	require.Error(t, err)
}

func TestProviderPreviewKeysPreserveVenueSemantics(t *testing.T) {
	require.Equal(t,
		[]string{"venue_spot", "spot-tickers", "spot-tickers-preview"},
		func() []string {
			kind, formal, preview := providerPreviewKeys("binance")
			return []string{kind, formal, preview}
		}(),
	)
	require.Equal(t,
		[]string{"perp_mark", "metaAndAssetCtxs", "metaAndAssetCtxs-preview"},
		func() []string {
			kind, formal, preview := providerPreviewKeys("hyperliquid")
			return []string{kind, formal, preview}
		}(),
	)
	require.Equal(t,
		[]string{"dex_route", "route-quotes", "route-quotes-preview"},
		func() []string {
			kind, formal, preview := providerPreviewKeys("uniswap")
			return []string{kind, formal, preview}
		}(),
	)
}

func TestDashboardDisplayKeepsDexRouteOutOfReferenceExpressions(t *testing.T) {
	for _, venue := range []string{"uniswap", "pancakeswap"} {
		display := dashboardDisplay(venue)

		require.NotContains(t, display.price, "venue_snapshot.price_usd")
		require.NotContains(t, display.priceKind, "'dex_route'")
		require.NotContains(t, display.change, "venue_snapshot.change_24h_pct")
		require.NotContains(t, display.changeKind, "'dex_route'")
		require.NotContains(t, display.observedAt, "venue_snapshot.last_success_at")
		require.Contains(t, display.price, "composite.price_usd")
		require.Contains(t, display.price, "am.reference_price_usd")
		require.True(t, strings.Contains(display.dexRoute, "venue_snapshot.price_usd IS NOT NULL"))
	}
}
