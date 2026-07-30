package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"

	"github.com/the-web3/s78-market-services/database"
)

func TestDashboardPriceFactsKeepVenueAndCompositeProvenanceSeparate(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	venueObservedAt := now.Add(-2 * time.Second)
	compositeObservedAt := now.Add(-time.Second)
	sourceTime := now.Add(-3 * time.Second)
	row := database.AssetIndexDashboardRow{
		Price:                textPtr("64173.20"),
		Change24hPct:         textPtr("-0.19"),
		Turnover24hUSD:       textPtr("500000000"),
		PriceKind:            "venue_spot",
		SourceTime:           &sourceTime,
		ObservedAt:           &venueObservedAt,
		LastSuccessAt:        &venueObservedAt,
		FreshnessStatus:      "fresh",
		Quality:              "low",
		Confidence:           "low",
		ContributorCount:     1,
		VenuePriceVersion:    41,
		CompositePrice:       textPtr("64203.13"),
		CompositeChange24h:   textPtr("1.25"),
		CompositeTurnover24h: textPtr("900000000"),
		CompositeCount:       2,
		CompositeConfidence:  "medium",
		CompositeContributors: datatypes.JSON([]byte(`[
			{"provider":"binance"},
			{"provider":"coinbase"},
			{"provider":"coinbase"}
		]`)),
		CompositeObservedAt: &compositeObservedAt,
		CompositeVersion:    99,
	}

	venuePrice, dexRoutePrice, displayPrice := dashboardPriceFacts(row, "coinbase", now)

	require.True(t, venuePrice.Available)
	require.Equal(t, "venue_spot", venuePrice.Kind)
	require.Equal(t, "coinbase", venuePrice.Source)
	require.Equal(t, []string{"coinbase"}, venuePrice.Contributors)
	require.Equal(t, 41, int(venuePrice.Version))
	require.Equal(t, venueObservedAt.UnixMilli(), venuePrice.ObservedAt)
	require.False(t, dexRoutePrice.Available)

	require.True(t, displayPrice.Available)
	require.Equal(t, "composite_reference", displayPrice.Kind)
	require.Equal(t, "cex_composite", displayPrice.Source)
	require.Equal(t, "medium", displayPrice.Quality)
	require.Equal(t, 2, displayPrice.ContributorCount)
	require.Equal(t, []string{"binance", "coinbase"}, displayPrice.Contributors)
	require.Equal(t, 99, int(displayPrice.Version))
	require.NotEqual(t, venuePrice.PriceUSD, displayPrice.PriceUSD)
}

func TestDashboardPriceFactsKeepDexRouteAndReferenceInSeparateLanes(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	routeObservedAt := now.Add(-45 * time.Second)
	compositeObservedAt := now.Add(-2 * time.Second)
	row := database.AssetIndexDashboardRow{
		Price:                textPtr("64211.10"),
		Change24hPct:         textPtr("0.42"),
		Turnover24hUSD:       textPtr("1000000"),
		PriceKind:            "dex_route",
		ObservedAt:           &routeObservedAt,
		LastSuccessAt:        &routeObservedAt,
		DexRouteAvailable:    true,
		Quality:              "high",
		ContributorCount:     2,
		VenuePriceVersion:    17,
		CompositePrice:       textPtr("64203.13"),
		CompositeChange24h:   textPtr("1.25"),
		CompositeTurnover24h: textPtr("900000000"),
		CompositeCount:       4,
		CompositeConfidence:  "high",
		CompositeContributors: datatypes.JSON([]byte(`[
			{"provider":"binance"},
			{"provider":"coinbase"},
			{"provider":"bybit"},
			{"provider":"okx"}
		]`)),
		CompositeObservedAt: &compositeObservedAt,
		CompositeVersion:    88,
	}

	venuePrice, dexRoutePrice, displayPrice := dashboardPriceFacts(row, "uniswap", now)

	require.False(t, venuePrice.Available)
	require.True(t, dexRoutePrice.Available)
	require.Equal(t, "dex_route", dexRoutePrice.Kind)
	require.Equal(t, "uniswap", dexRoutePrice.Source)
	require.Equal(t, "stale", dexRoutePrice.FreshnessStatus)
	require.True(t, dexRoutePrice.Change24hPct.Available)

	require.True(t, displayPrice.Available)
	require.Equal(t, "composite_reference", displayPrice.Kind)
	require.Equal(t, "cex_composite", displayPrice.Source)
	require.NotEqual(t, dexRoutePrice.PriceUSD, displayPrice.PriceUSD)
}

func TestDashboardPriceFactsReferenceFallbackDoesNotReviveExpiredDexRoute(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expiredRouteAt := now.Add(-90 * time.Second)
	referenceObservedAt := now.Add(-10 * time.Minute)
	referenceSourceTime := referenceObservedAt.Add(-time.Second)
	row := database.AssetIndexDashboardRow{
		Price:                textPtr("64000"),
		Change24hPct:         textPtr("9.5"),
		PriceKind:            "dex_route",
		ObservedAt:           &expiredRouteAt,
		LastSuccessAt:        &expiredRouteAt,
		DexRouteAvailable:    true,
		MarketReferencePrice: textPtr("64100"),
		ReferenceObservedAt:  &referenceObservedAt,
		ReferenceSourceTime:  &referenceSourceTime,
	}

	_, dexRoutePrice, displayPrice := dashboardPriceFacts(row, "pancakeswap", now)

	require.False(t, dexRoutePrice.Available)
	require.Equal(t, "unavailable", dexRoutePrice.Kind)
	require.Empty(t, dexRoutePrice.Source)
	require.False(t, dexRoutePrice.Change24hPct.Available)
	require.True(t, displayPrice.Available)
	require.Equal(t, "market_reference", displayPrice.Kind)
	require.Equal(t, "coingecko", displayPrice.Source)
	require.Equal(t, "stale", displayPrice.FreshnessStatus)
	require.False(t, displayPrice.Change24hPct.Available)
}

func TestTickPriceFactCarriesCompositeContributors(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Second)
	row := database.MarketPriceTickRow{
		Provider:         "all",
		PriceKind:        "composite_spot",
		PriceUSD:         textPtr("64203.13"),
		Change24hPct:     textPtr("1.25"),
		Turnover24hUSD:   textPtr("900000000"),
		ContributorCount: 2,
		Confidence:       "medium",
		Quality:          "medium",
		Contributors: datatypes.JSON([]byte(`[
			{"provider":"binance"},
			{"provider":"coinbase"}
		]`)),
		Available:     true,
		ObservedAt:    &observedAt,
		LastSuccessAt: &observedAt,
		Version:       101,
	}

	fact := tickPriceFact(row, "all", now)

	require.True(t, fact.Available)
	require.Equal(t, "composite_reference", fact.Kind)
	require.Equal(t, "cex_composite", fact.Source)
	require.Equal(t, []string{"binance", "coinbase"}, fact.Contributors)
	require.Equal(t, int64(0), fact.SourceTime)
	require.Equal(t, int64(101), fact.Version)
}

func textPtr(value string) *string {
	return &value
}
