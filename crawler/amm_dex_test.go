package crawler

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
)

func TestEncodeV3PathPreservesTokenAndFeeOrder(t *testing.T) {
	tokens := []database.AssetRepresentation{
		{ContractAddress: "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"},
		{ContractAddress: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"},
	}
	path, err := encodeV3Path(tokens, []int{3000})
	require.NoError(t, err)
	require.Len(t, path, 43)
	require.Equal(t, "000bb8", hex.EncodeToString(path[20:23]))
	require.Equal(t, tokens[1].ContractAddress[2:], hex.EncodeToString(path[23:]))
}

func TestBuildAMMRoutesUsesReviewedDirectPool(t *testing.T) {
	asset := database.AssetRepresentation{
		AssetGuid: "eth", ChainID: 1,
		ContractAddress: uniswapAMMConfig.BridgeAddress, TokenSymbol: "WETH", Decimals: 18,
	}
	stable := database.AssetRepresentation{
		AssetGuid: "usdc", ChainID: 1,
		ContractAddress: uniswapAMMConfig.StableAddress, TokenSymbol: "USDC", Decimals: 6,
	}
	pool := discoveredPool{
		Address: "0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640", ProtocolVersion: "v3",
		Token0: stable.ContractAddress, Token1: asset.ContractAddress,
		Token0Price: decimal.RequireFromString("0.00025"),
		Token1Price: decimal.RequireFromString("4000"),
		Fee:         500, TVLUSD: decimal.NewFromInt(20_000_000),
	}
	routes := buildAMMRoutes(uniswapAMMConfig, []database.AssetRepresentation{asset, stable}, []discoveredPool{pool})
	require.Len(t, routes, 1)
	require.Len(t, routes[0].Pools, 1)
	require.Equal(t, asset.AssetGuid, routes[0].Asset.AssetGuid)
}

func TestBuildAMMRoutesKeepsV2V3AndMixedTwoHopCandidates(t *testing.T) {
	asset := database.AssetRepresentation{
		AssetGuid: "asset", ChainID: 1,
		ContractAddress: "0x0000000000000000000000000000000000000001",
		TokenSymbol:     "ASSET", Decimals: 18,
	}
	bridge := database.AssetRepresentation{
		AssetGuid: "weth", ChainID: 1,
		ContractAddress: uniswapAMMConfig.BridgeAddress,
		TokenSymbol:     "WETH", Decimals: 18,
	}
	stable := database.AssetRepresentation{
		AssetGuid: "usdc", ChainID: 1,
		ContractAddress: uniswapAMMConfig.StableAddress,
		TokenSymbol:     "USDC", Decimals: 6,
	}
	pool := func(address, protocol, left, right string, tvl int64) discoveredPool {
		return discoveredPool{
			Address: address, ProtocolVersion: protocol,
			Token0: left, Token1: right, Fee: 3000,
			TVLUSD:       decimal.NewFromInt(tvl),
			Volume24hUSD: decimal.NewFromInt(1_000_000),
		}
	}
	pools := []discoveredPool{
		pool("0x0000000000000000000000000000000000000011", "v2",
			asset.ContractAddress, stable.ContractAddress, 4_000_000),
		pool("0x0000000000000000000000000000000000000012", "v3",
			asset.ContractAddress, stable.ContractAddress, 5_000_000),
		pool("0x0000000000000000000000000000000000000013", "v2",
			asset.ContractAddress, bridge.ContractAddress, 3_000_000),
		pool("0x0000000000000000000000000000000000000014", "v3",
			bridge.ContractAddress, stable.ContractAddress, 6_000_000),
	}
	routes := buildAMMRoutes(
		uniswapAMMConfig,
		[]database.AssetRepresentation{asset, bridge, stable},
		pools,
	)
	var assetRoutes []ammRoute
	for _, route := range routes {
		if route.Asset.AssetGuid == asset.AssetGuid {
			assetRoutes = append(assetRoutes, route)
		}
	}
	require.Len(t, assetRoutes, 3)
	var directProtocols []string
	var mixedRoute *ammRoute
	for _, route := range assetRoutes {
		require.LessOrEqual(t, len(route.Pools), 2)
		if len(route.Pools) == 1 {
			directProtocols = append(
				directProtocols, route.Pools[0].ProtocolVersion,
			)
		} else {
			copy := route
			mixedRoute = &copy
		}
	}
	require.ElementsMatch(t, []string{"v2", "v3"}, directProtocols)
	require.NotNil(t, mixedRoute)
	require.Equal(t, "v2", mixedRoute.Pools[0].ProtocolVersion)
	require.Equal(t, "v3", mixedRoute.Pools[1].ProtocolVersion)
}

func TestBuildAMMRoutesCapsCandidatesPerAsset(t *testing.T) {
	asset := database.AssetRepresentation{
		AssetGuid: "asset", ChainID: 1,
		ContractAddress: "0x0000000000000000000000000000000000000001",
	}
	stable := database.AssetRepresentation{
		AssetGuid: "usdc", ChainID: 1,
		ContractAddress: uniswapAMMConfig.StableAddress,
	}
	pools := make([]discoveredPool, 0, dexMaxRoutesPerAsset+4)
	for index := 0; index < dexMaxRoutesPerAsset+4; index++ {
		pools = append(pools, discoveredPool{
			Address:         fmt.Sprintf("0x%040x", index+100),
			ProtocolVersion: "v3", Token0: asset.ContractAddress,
			Token1: stable.ContractAddress, Fee: 100 + index,
			TVLUSD:       decimal.NewFromInt(int64(1_000_000 + index)),
			Volume24hUSD: decimal.NewFromInt(1_000_000),
		})
	}
	routes := buildAMMRoutes(
		uniswapAMMConfig,
		[]database.AssetRepresentation{asset, stable},
		pools,
	)
	require.Len(t, routes, dexMaxRoutesPerAsset)
}

func TestMergeDeferredAMMRoutesRetainsOnlyAffectedReviewedRoutes(t *testing.T) {
	deferredToken := database.AssetRepresentation{
		AssetGuid:       "deferred",
		ContractAddress: "0x0000000000000000000000000000000000000001",
	}
	healthyToken := database.AssetRepresentation{
		AssetGuid:       "healthy",
		ContractAddress: "0x0000000000000000000000000000000000000002",
	}
	current := []ammRoute{{
		Asset: healthyToken, Tokens: []database.AssetRepresentation{healthyToken},
		RouteKey: "healthy-current",
	}}
	previous := []ammRoute{
		{
			Asset:    deferredToken,
			Tokens:   []database.AssetRepresentation{deferredToken},
			RouteKey: "deferred-previous",
		},
		{
			Asset:    healthyToken,
			Tokens:   []database.AssetRepresentation{healthyToken},
			RouteKey: "healthy-obsolete",
		},
	}
	merged := mergeDeferredAMMRoutes(current, previous, map[string]struct{}{
		deferredToken.ContractAddress: {},
	})
	require.Len(t, merged, 2)
	require.Equal(t, "deferred-previous", merged[0].RouteKey)
	require.Equal(t, "healthy-current", merged[1].RouteKey)
}

func TestBetterDexRoutePrefersAvailabilityThenQuality(t *testing.T) {
	lowTVL := "100"
	highTVL := "1000"
	require.True(t, betterDexRoute(
		database.DexRouteCurrent{Available: true, Quality: "medium", TVLUSD: &lowTVL},
		database.DexRouteCurrent{Available: false, Quality: "high", TVLUSD: &highTVL},
	))
	require.True(t, betterDexRoute(
		database.DexRouteCurrent{Available: true, Quality: "high", TVLUSD: &lowTVL},
		database.DexRouteCurrent{Available: true, Quality: "medium", TVLUSD: &highTVL},
	))
}

func TestAssessDexQuoteRejectsImpactSpreadAndDivergence(t *testing.T) {
	tests := []struct {
		name       string
		impact     string
		spread     string
		divergence string
		reason     string
	}{
		{name: "impact", impact: "1.01", spread: "0.1", divergence: "0.1", reason: "price_impact_gt_1pct"},
		{name: "spread", impact: "0.1", spread: "2.01", divergence: "0.1", reason: "round_trip_spread_gt_2pct"},
		{name: "divergence", impact: "0.1", spread: "0.1", divergence: "3.01", reason: "cex_divergence_gt_3pct"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			available, quality, reason := assessDexQuote(
				decimal.RequireFromString(test.impact),
				decimal.RequireFromString(test.spread),
				decimalPointer(test.divergence), 10_000,
				1, decimal.NewFromInt(20_000_000), decimal.NewFromInt(2_000_000),
			)
			require.False(t, available)
			require.Equal(t, "unknown", quality)
			require.NotNil(t, reason)
			require.Equal(t, test.reason, *reason)
		})
	}
	available, quality, reason := assessDexQuote(
		decimal.RequireFromString("0.4"), decimal.RequireFromString("0.8"),
		decimalPointer("0.2"), 10_000, 1,
		decimal.NewFromInt(20_000_000), decimal.NewFromInt(2_000_000),
	)
	require.True(t, available)
	require.Equal(t, "high", quality)
	require.Nil(t, reason)
}

func TestAssessDexQuoteAllowsHonestOnchainOnlyFallback(t *testing.T) {
	available, quality, reason := assessDexQuote(
		decimal.RequireFromString("0.4"), decimal.RequireFromString("0.8"),
		nil, 10_000, 1,
		decimal.NewFromInt(20_000_000), decimal.NewFromInt(2_000_000),
	)
	require.True(t, available)
	require.Equal(t, "medium", quality)
	require.Nil(t, reason)
}

func TestDexQuotePriceMetricsUseSameBlockBidirectionalMid(t *testing.T) {
	mid, spread, impact := dexQuotePriceMetrics(
		decimal.NewFromInt(101), decimal.NewFromInt(99),
	)
	require.Equal(t, "100", mid.String())
	require.Equal(t, "2", spread.String())
	require.Equal(t, "1", impact.String())
}

func TestAssessDexQuoteCapsSmallerNotionalAtLowQuality(t *testing.T) {
	for _, notional := range []int64{1_000, 100} {
		available, quality, reason := assessDexQuote(
			decimal.RequireFromString("0.2"), decimal.RequireFromString("0.4"),
			decimalPointer("0.1"), notional, 1,
			decimal.NewFromInt(20_000_000), decimal.NewFromInt(2_000_000),
		)
		require.True(t, available)
		require.Equal(t, "low", quality)
		require.Nil(t, reason)
	}
}

func TestRetryableDexQuoteFailureOnlyRetriesSizeSensitiveFailures(t *testing.T) {
	require.True(t, retryableDexQuoteFailure(textPtr("price_impact_gt_1pct")))
	require.True(t, retryableDexQuoteFailure(textPtr("buy_quote_failed")))
	require.False(t, retryableDexQuoteFailure(textPtr("block_stale")))
	require.False(t, retryableDexQuoteFailure(nil))
}

func TestDexCoverageRequiresFullRouteSpecificObservationWindow(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	start := now.Add(-24 * time.Hour)
	coverage := &database.DexWindowCoverage{
		FirstObservedAt:  start.Add(20 * time.Minute),
		LastObservedAt:   now.Add(-dexQuoteInterval),
		OpenPriceUSD:     "100",
		ObservationCount: 5600,
		MaxGap:           time.Minute,
	}
	require.True(t, dexCoverageSufficient(coverage, start, now))

	coverage.FirstObservedAt = start.Add(31 * time.Minute)
	require.False(t, dexCoverageSufficient(coverage, start, now))
	coverage.FirstObservedAt = start.Add(20 * time.Minute)
	coverage.MaxGap = 2*time.Minute + time.Second
	require.False(t, dexCoverageSufficient(coverage, start, now))
}

func decimalPointer(value string) *decimal.Decimal {
	parsed := decimal.RequireFromString(value)
	return &parsed
}

func TestAMMAdapterRetriesInitialRPCFailure(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server.Close()

	ready := make(chan struct{})
	adapter := &AMMAdapter{
		config: ammProviderConfig{
			Provider: "uniswap", ChainID: 1,
			RPCURL: server.URL, SubgraphURL: "http://unused.invalid",
		},
		rpcRetryMin: 5 * time.Millisecond,
		rpcRetryMax: 10 * time.Millisecond,
		connectionReady: func(context.Context) {
			close(ready)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	adapter.Start(ctx)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("AMM adapter did not recover its RPC session")
	}
	require.GreaterOrEqual(t, calls.Load(), int64(2))
	adapter.Stop()
}

func TestSubgraphFailuresAreClassifiedWithoutRPCFallback(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{}`},
		{name: "malformed", status: http.StatusOK, body: `{not-json`},
		{name: "graphql error", status: http.StatusOK, body: `{"errors":[{"message":"denied"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			adapter := &AMMAdapter{
				config:     ammProviderConfig{SubgraphURL: server.URL},
				httpClient: server.Client(),
			}
			_, err := adapter.discoverPoolsForToken(context.Background(), uniswapAMMConfig.BridgeAddress)
			require.Error(t, err)
		})
	}
}

func TestNewDexSupervisorUsesPublicFallbackOnlyWhenExplicitlyEnabled(t *testing.T) {
	configs := buildAMMProviderConfigs(&config.Config{
		DexPublicFallback: true,
	})
	require.Equal(t, publicEthereumRPCURL, configs[0].RPCURL)
	require.Equal(t, publicBSCRPCURL, configs[1].RPCURL)
	require.True(t, configs[0].PublicDiscovery)
	require.True(t, configs[1].PublicDiscovery)
	require.Equal(t, dexScreenerAPIURL, configs[0].DiscoveryURL)

	disabled := buildAMMProviderConfigs(&config.Config{})
	require.Empty(t, disabled[0].RPCURL)
	require.False(t, disabled[0].PublicDiscovery)
}
