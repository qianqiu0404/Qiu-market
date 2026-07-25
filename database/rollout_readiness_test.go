package database

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormalObservationReadinessAcceptsExactCanaryBoundary(t *testing.T) {
	startedAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	now := startedAt.Add(24 * time.Hour)
	status := &ProviderStatus{
		ObservationStartedAt: &startedAt,
		AttemptCount:         100,
		SuccessCount:         99,
	}
	state := &ProviderRolloutState{
		Mode: "canary", LastTransitionAt: startedAt,
	}
	result := &ProviderRolloutReadiness{}

	appendFormalObservationBlockers(result, status, state, 99, now)

	require.Empty(t, result.Blockers)
	require.NotNil(t, result.ReadinessNotBefore)
	require.Equal(t, now, *result.ReadinessNotBefore)
}

func TestFormalObservationReadinessRejectsIncompleteEvidence(t *testing.T) {
	startedAt := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	status := &ProviderStatus{
		ObservationStartedAt: &startedAt,
		AttemptCount:         99,
		SuccessCount:         97,
	}
	state := &ProviderRolloutState{
		Mode: "canary", LastTransitionAt: startedAt,
	}
	result := &ProviderRolloutReadiness{}

	appendFormalObservationBlockers(
		result, status, state, 99, startedAt.Add(23*time.Hour),
	)

	joined := strings.Join(result.Blockers, "\n")
	require.Contains(t, joined, "observation is incomplete")
	require.Contains(t, joined, "need at least 100")
	require.Contains(t, joined, "below 99.00%")
}

func TestFormalObservationReadinessRejectsImpossibleCounters(t *testing.T) {
	startedAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	status := &ProviderStatus{
		ObservationStartedAt: &startedAt,
		AttemptCount:         100,
		SuccessCount:         101,
	}
	state := &ProviderRolloutState{
		Mode: "canary", LastTransitionAt: startedAt,
	}
	result := &ProviderRolloutReadiness{}

	appendFormalObservationBlockers(
		result, status, state, 99, startedAt.Add(24*time.Hour),
	)

	require.Contains(t, strings.Join(result.Blockers, "\n"), "counters are inconsistent")
	require.Equal(t, "100.00", providerSuccessRateText(100, 101))
}

func TestFeedStatusReadinessRejectsFailureAndStaleness(t *testing.T) {
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-31 * time.Second)
	status := &ProviderStatus{
		LastSuccessAt:       &lastSuccess,
		ConsecutiveFailures: 1,
	}
	result := &ProviderRolloutReadiness{}

	appendFeedStatusBlockers(
		result, status, "spot-tickers", 30*time.Second, now,
	)

	joined := strings.Join(result.Blockers, "\n")
	require.Contains(t, joined, "is stale")
	require.Contains(t, joined, "1 consecutive failures")
}
