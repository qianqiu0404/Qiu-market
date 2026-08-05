package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"

	"github.com/the-web3/s78-market-services/database"
)

func TestAggregateProviderStatusesUsesActiveCapabilityInsteadOfWorstSource(t *testing.T) {
	now := time.Date(2026, 7, 24, 4, 0, 0, 0, time.UTC)
	success := now.Add(-10 * time.Second)
	staleSuccess := now.Add(-30 * time.Minute)
	attempt := now.Add(-20 * time.Minute)
	errorClass := "network"
	rows := []database.ProviderStatus{
		{
			Provider: "binance", SourceKey: "spot-tickers",
			LastAttemptAt: &success, LastSuccessAt: &success,
			AttemptCount: 100, SuccessCount: 100,
		},
		{
			Provider: "binance", SourceKey: "klines",
			LastAttemptAt: &attempt, LastSuccessAt: &staleSuccess,
			ConsecutiveFailures: 2, LastErrorClass: &errorClass,
		},
	}
	rollouts := []database.ProviderRolloutState{{
		Provider: "binance", Mode: "canary", RankLimit: 50,
	}}
	result := aggregateProviderStatuses(rows, rollouts, now)
	require.Len(t, result, 1)
	require.Equal(t, "Healthy", result[0].Status)
	require.Equal(t, "spot-tickers", result[0].PrimarySourceKey)
	require.EqualValues(t, 1, result[0].FailingSourceCount)
	require.Equal(t, "Stale", result[0].Sources[1].Status)
}

func TestProviderStatusUsesSourceSpecificCadence(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	catalogSuccess := now.Add(-6 * time.Hour)
	rows := []database.ProviderStatus{
		{
			Provider: "coinbase", SourceKey: "catalog",
			LastAttemptAt: &catalogSuccess, LastSuccessAt: &catalogSuccess,
			AttemptCount: 2, SuccessCount: 2,
		},
		{
			Provider: "coinbase", SourceKey: "spot-tickers-shadow",
			LastAttemptAt: &now, LastSuccessAt: &now,
			AttemptCount: 100, SuccessCount: 100,
		},
	}
	rollouts := []database.ProviderRolloutState{{
		Provider: "coinbase", Mode: "shadow", RankLimit: 50,
	}}
	result := aggregateProviderStatuses(rows, rollouts, now)
	require.Len(t, result, 1)
	require.Equal(t, "Observing", result[0].Status)
	require.Equal(t, "100.00", result[0].SuccessRatePct)
	require.Equal(t, "Healthy", result[0].Sources[0].Status)
	require.Equal(t, "Healthy", result[0].Sources[1].Status)
	require.Equal(t, "realtime", result[0].Sources[1].Capability)
}

func TestAggregateProviderStatusesKeepsLocalPreviewDistinctFromFormalRollout(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	rows := []database.ProviderStatus{{
		Provider: "coinbase", SourceKey: "spot-tickers-preview",
		LastAttemptAt: &now, LastSuccessAt: &now,
		AttemptCount: 3, SuccessCount: 3,
		Details: datatypes.JSON([]byte(`{
			"received_count":100,
			"matched_asset_count":20,
			"price_available_count":19,
			"change_available_count":18
		}`)),
	}}
	rollouts := []database.ProviderRolloutState{{
		Provider: "coinbase", Mode: "shadow", RankLimit: 50,
		LocalPreviewEnabled: true,
	}}
	result := aggregateProviderStatuses(rows, rollouts, now)
	require.Len(t, result, 1)
	require.Equal(t, "Local Preview", result[0].Status)
	require.Equal(t, "Healthy", result[0].OperationalStatus)
	require.Equal(t, "shadow", result[0].RolloutMode)
	require.Equal(t, "spot-tickers-preview", result[0].PrimarySourceKey)
	require.EqualValues(t, 19, result[0].PreviewCoveredCount)
}

func TestCoinbaseRESTFallbackIsARealtimeCapability(t *testing.T) {
	require.Equal(t, "realtime", providerSourceCapability("spot-tickers-rest-fallback"))
	now := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	require.Equal(t, "websocket_primary_rest_reconcile", providerFeedMode(
		"coinbase",
		"spot-tickers-preview",
		map[string]database.ProviderStatus{
			"spot-tickers-preview": {
				Provider: "coinbase", SourceKey: "spot-tickers-preview",
				LastSuccessAt: &now,
			},
			"spot-tickers-rest-fallback": {
				Provider: "coinbase", SourceKey: "spot-tickers-rest-fallback",
				LastSuccessAt: &now,
			},
		},
	))
}

func TestAggregateProviderStatusesReportsUnconfiguredDEX(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	errorClass := "unconfigured"
	rows := []database.ProviderStatus{{
		Provider: "uniswap", SourceKey: "route-quotes",
		LastAttemptAt: &now, ConsecutiveFailures: 1, LastErrorClass: &errorClass,
	}}
	rollouts := []database.ProviderRolloutState{{
		Provider: "uniswap", Mode: "shadow", RankLimit: 50,
	}}
	result := aggregateProviderStatuses(rows, rollouts, now)
	require.Len(t, result, 1)
	require.Equal(t, "Unconfigured", result[0].Status)
}

func TestPrimaryProviderSourceKeyUsesDEXPreviewFeeds(t *testing.T) {
	require.Equal(t,
		"metaAndAssetCtxs-preview",
		primaryProviderSourceKey("hyperliquid", "enabled", true),
	)
	require.Equal(t,
		"route-quotes-preview",
		primaryProviderSourceKey("uniswap", "shadow", true),
	)
	require.Equal(t,
		"route-quotes-preview",
		primaryProviderSourceKey("pancakeswap", "shadow", true),
	)
	require.Equal(t,
		"route-quotes",
		primaryProviderSourceKey("uniswap", "shadow", false),
	)
}

func TestAggregateProviderStatusesExposesCEXFeedAndKlineEvidence(t *testing.T) {
	now := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	rows := []database.ProviderStatus{
		{
			Provider: "okx", SourceKey: "spot-tickers-preview",
			LastAttemptAt: &now, LastSuccessAt: &now,
		},
		{
			Provider: "okx", SourceKey: "spot-tickers-rest-reconcile",
			LastAttemptAt: &now, LastSuccessAt: &now,
		},
		{
			Provider: "okx", SourceKey: "klines",
			LastAttemptAt: &now, LastSuccessAt: &now,
			Details: datatypes.JSON([]byte(`{
				"matched_asset_count":50,
				"received_count":150,
				"written_count":150
			}`)),
		},
	}
	rollouts := []database.ProviderRolloutState{{
		Provider: "okx", Mode: "shadow", RankLimit: 50,
		LocalPreviewEnabled: true,
	}}
	result := aggregateProviderStatuses(rows, rollouts, now)
	require.Len(t, result, 1)
	require.Equal(
		t, "websocket_primary_rest_reconcile", result[0].FeedMode,
	)
	require.Equal(t, "Healthy", result[0].KlineStatus)
	require.EqualValues(t, 50, result[0].KlineMarketCount)
	require.EqualValues(t, 150, result[0].KlineCandleCount)
	require.EqualValues(t, now.UnixMilli(), result[0].KlineLastSuccessAt)
	require.EqualValues(t, 150, result[0].Sources[2].WrittenCount)
}
