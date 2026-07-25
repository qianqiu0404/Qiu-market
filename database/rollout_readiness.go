package database

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ProviderRolloutReadiness struct {
	Provider             string     `json:"provider"`
	CurrentMode          string     `json:"current_mode"`
	Target               string     `json:"target"`
	PrimarySourceKey     string     `json:"primary_source_key"`
	RankLimit            int        `json:"rank_limit"`
	CanaryAssetIDs       []string   `json:"canary_asset_ids"`
	ObservationStartedAt *time.Time `json:"observation_started_at,omitempty"`
	ReadinessNotBefore   *time.Time `json:"readiness_not_before,omitempty"`
	AttemptCount         int64      `json:"attempt_count"`
	SuccessCount         int64      `json:"success_count"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	SuccessRatePct       string     `json:"success_rate_pct"`
	ReceivedCount        int64      `json:"received_count"`
	MatchedAssetCount    int64      `json:"matched_asset_count"`
	PriceAvailableCount  int64      `json:"price_available_count"`
	ChangeAvailableCount int64      `json:"change_available_count"`
	WrittenCount         int64      `json:"written_count"`
	Ready                bool       `json:"ready"`
	Blockers             []string   `json:"blockers"`
	LocalPreviewEnabled  bool       `json:"local_preview_enabled"`
	PreviewSourceKey     string     `json:"preview_source_key,omitempty"`
}

func (r *ProviderRolloutReadiness) addBlocker(format string, args ...interface{}) {
	r.Blockers = append(r.Blockers, fmt.Sprintf(format, args...))
}

func EvaluateProviderRolloutReadiness(
	db *DB,
	provider string,
	rankLimit int,
	now time.Time,
) (*ProviderRolloutReadiness, error) {
	provider = normalizedProvider(provider)
	if rankLimit < 1 {
		rankLimit = 50
	}
	state, err := db.MarketAggregation.QueryProviderRollout(provider)
	if err != nil {
		return nil, err
	}
	result := &ProviderRolloutReadiness{
		Provider: provider, RankLimit: rankLimit, Blockers: []string{},
	}
	if state == nil {
		result.CurrentMode = "unconfigured"
		result.Target = "canary"
		result.addBlocker("provider rollout is not configured")
		return result, nil
	}
	result.CurrentMode = state.Mode
	result.RankLimit = state.RankLimit
	result.LocalPreviewEnabled = state.LocalPreviewEnabled
	if state.LocalPreviewEnabled {
		_, _, result.PreviewSourceKey = providerPreviewKeys(provider)
		result.addBlocker("local preview is active; disable it to start formal rollout observation")
	}
	switch state.Mode {
	case "shadow", "paused":
		result.Target = "canary"
	case "canary":
		result.Target = "enabled"
	case "enabled":
		result.Target = "stable"
	default:
		result.Target = "canary"
		result.addBlocker("unsupported current rollout mode %q", state.Mode)
	}
	if result.Target != "stable" {
		if err := appendPredecessorReadinessBlocker(
			db, result, provider, now,
		); err != nil {
			return nil, err
		}
	}

	sourceKey, priceKind, freshness, minimumRate := readinessFeedPolicy(provider, state.Mode)
	if state.LocalPreviewEnabled {
		_, _, sourceKey = providerPreviewKeys(provider)
		if isCEXSelectionProvider(provider) {
			freshness = 30 * time.Second
		} else {
			freshness = time.Minute
		}
	}
	result.PrimarySourceKey = sourceKey

	if result.Target == "canary" {
		if err := appendCatalogReadinessBlockers(db, result, now); err != nil {
			return nil, err
		}
		eligible, err := db.MarketAggregation.QueryEligibleProviderAssetIDs(provider, state.RankLimit)
		if err != nil {
			return nil, err
		}
		if len(eligible) < 10 {
			result.addBlocker("only %d reviewed eligible assets are available; canary requires 10", len(eligible))
		} else {
			result.CanaryAssetIDs = append([]string(nil), eligible[:10]...)
		}
	} else {
		canaryIDs, err := decodeRolloutAssetIDs(state.CanaryAssetIDs)
		if err != nil {
			result.addBlocker("persisted canary asset list is invalid: %v", err)
		} else if state.Mode == "canary" {
			result.CanaryAssetIDs = canaryIDs
			if len(canaryIDs) != 10 {
				result.addBlocker("canary must contain exactly 10 persisted assets")
			}
		}
	}

	status, err := db.ProviderStatus.QueryProviderStatus(provider, sourceKey)
	if err != nil {
		return nil, err
	}
	if status == nil {
		result.addBlocker("no %s observation has been recorded", sourceKey)
		result.Ready = false
		return result, nil
	}
	result.ObservationStartedAt = cloneTime(status.ObservationStartedAt)
	result.AttemptCount = status.AttemptCount
	result.SuccessCount = status.SuccessCount
	result.ConsecutiveFailures = status.ConsecutiveFailures
	result.SuccessRatePct = providerSuccessRateText(status.AttemptCount, status.SuccessCount)
	details := status.ParsedDetails()
	result.ReceivedCount = details.ReceivedCount
	result.MatchedAssetCount = details.MatchedAssetCount
	result.PriceAvailableCount = details.PriceAvailableCount
	result.ChangeAvailableCount = details.ChangeAvailableCount
	result.WrittenCount = details.WrittenCount

	appendFeedStatusBlockers(result, status, sourceKey, freshness, now)

	if result.Target == "canary" {
		if details.MatchedAssetCount < 10 {
			result.addBlocker("shadow feed matches %d assets; need at least 10", details.MatchedAssetCount)
		}
		if details.PriceAvailableCount < 10 {
			result.addBlocker("shadow feed has valid prices for %d assets; need at least 10", details.PriceAvailableCount)
		}
		if details.ChangeAvailableCount < 10 {
			result.addBlocker("shadow feed has 24h references for %d assets; need at least 10", details.ChangeAvailableCount)
		}
	} else {
		appendFormalObservationBlockers(result, status, state, minimumRate, now)
		if err := appendFormalSnapshotBlockers(
			db, result, provider, priceKind, freshness, now,
		); err != nil {
			return nil, err
		}
		if provider == "binance" && state.Mode == "canary" {
			if err := appendDWReadinessBlockers(db, result, now); err != nil {
				return nil, err
			}
		}
	}
	result.Ready = len(result.Blockers) == 0
	return result, nil
}

func appendPredecessorReadinessBlocker(
	db *DB,
	result *ProviderRolloutReadiness,
	provider string,
	now time.Time,
) error {
	predecessor := map[string]string{
		"coinbase":    "binance",
		"bybit":       "coinbase",
		"okx":         "bybit",
		"uniswap":     "okx",
		"pancakeswap": "uniswap",
	}[provider]
	if predecessor == "" {
		return nil
	}
	state, err := db.MarketAggregation.QueryProviderRollout(predecessor)
	if err != nil {
		return err
	}
	if state == nil || state.Mode != "enabled" {
		result.addBlocker("predecessor %s is not enabled and stable", predecessor)
		return nil
	}
	readiness, err := EvaluateProviderRolloutReadiness(
		db, predecessor, state.RankLimit, now,
	)
	if err != nil {
		return err
	}
	if readiness.Target != "stable" || !readiness.Ready {
		result.addBlocker("predecessor %s has not completed its enabled observation", predecessor)
	}
	return nil
}

func appendFeedStatusBlockers(
	result *ProviderRolloutReadiness,
	status *ProviderStatus,
	sourceKey string,
	freshness time.Duration,
	now time.Time,
) {
	if status.LastSuccessAt == nil {
		result.addBlocker("%s has no successful observation", sourceKey)
	} else if now.Sub(status.LastSuccessAt.UTC()) > freshness {
		result.addBlocker("%s is stale; last success %s",
			sourceKey, status.LastSuccessAt.UTC().Format(time.RFC3339))
	}
	if status.ConsecutiveFailures > 0 {
		result.addBlocker("%s has %d consecutive failures", sourceKey, status.ConsecutiveFailures)
	}
}

func appendFormalObservationBlockers(
	result *ProviderRolloutReadiness,
	status *ProviderStatus,
	state *ProviderRolloutState,
	minimumRate float64,
	now time.Time,
) {
	required := time.Duration(readinessMinimumSoakHours(state.Mode)) * time.Hour
	if state.MinSoakUntil != nil {
		configured := state.MinSoakUntil.UTC().Sub(state.LastTransitionAt.UTC())
		if configured > required {
			required = configured
		}
	}
	if status.ObservationStartedAt == nil {
		result.addBlocker("real feed observation has not started in this rollout")
	} else {
		notBefore := status.ObservationStartedAt.UTC().Add(required)
		result.ReadinessNotBefore = &notBefore
		if status.ObservationStartedAt.Before(state.LastTransitionAt.Add(-time.Second)) {
			result.addBlocker("observation window predates the current rollout")
		}
		if now.Before(notBefore) {
			result.addBlocker("real feed observation is incomplete until %s", notBefore.Format(time.RFC3339))
		}
	}
	if status.AttemptCount < 100 {
		result.addBlocker("only %d feed attempts recorded; need at least 100", status.AttemptCount)
	}
	if status.SuccessCount > status.AttemptCount {
		result.addBlocker("feed counters are inconsistent: %d successes exceed %d attempts",
			status.SuccessCount, status.AttemptCount)
	}
	rate := providerSuccessRate(status.AttemptCount, status.SuccessCount)
	if rate < minimumRate {
		result.addBlocker("feed success rate %.2f%% is below %.2f%%", rate, minimumRate)
	}
}

func appendCatalogReadinessBlockers(
	db *DB,
	result *ProviderRolloutReadiness,
	now time.Time,
) error {
	sourceKey := "catalog"
	freshness := 7 * time.Hour
	switch result.Provider {
	case "uniswap", "pancakeswap":
		sourceKey = "pool-catalog"
	case "hyperliquid":
		sourceKey = "metaAndAssetCtxs"
		freshness = 2 * time.Minute
	}
	status, err := db.ProviderStatus.QueryProviderStatus(result.Provider, sourceKey)
	if err != nil {
		return err
	}
	if status == nil || status.LastSuccessAt == nil {
		result.addBlocker("catalog has no successful %s observation", sourceKey)
		return nil
	}
	if now.Sub(status.LastSuccessAt.UTC()) > freshness {
		result.addBlocker("%s is stale; last success %s",
			sourceKey, status.LastSuccessAt.UTC().Format(time.RFC3339))
	}
	if status.ConsecutiveFailures > 0 {
		result.addBlocker("%s has %d consecutive failures", sourceKey, status.ConsecutiveFailures)
	}
	return nil
}

func appendFormalSnapshotBlockers(
	db *DB,
	result *ProviderRolloutReadiness,
	provider, priceKind string,
	freshness time.Duration,
	now time.Time,
) error {
	allowed, _, err := db.MarketAggregation.QueryRolloutAssetIDs(provider)
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		result.addBlocker("rollout exposes no reviewed assets")
		return nil
	}
	evidence, err := db.MarketAggregation.QueryFreshVenueAssetEvidence(
		provider, priceKind, now.Add(-freshness),
	)
	if err != nil {
		return err
	}
	fresh := make(map[string]VenueAssetEvidence, len(evidence))
	for _, row := range evidence {
		fresh[row.AssetID] = row
	}
	var missingPrice, missingChange []string
	for assetID := range allowed {
		row, ok := fresh[assetID]
		if !ok {
			missingPrice = append(missingPrice, assetID)
			continue
		}
		if strings.EqualFold(priceKind, "venue_spot") && !row.HasChange {
			missingChange = append(missingChange, assetID)
		}
	}
	sort.Strings(missingPrice)
	sort.Strings(missingChange)
	if len(missingPrice) > 0 {
		result.addBlocker("fresh %s snapshot is missing for %d/%d assets: %s",
			priceKind, len(missingPrice), len(allowed), strings.Join(missingPrice, ","))
	}
	if len(missingChange) > 0 {
		result.addBlocker("24h reference is missing for %d/%d assets: %s",
			len(missingChange), len(allowed), strings.Join(missingChange, ","))
	}
	return nil
}

func appendDWReadinessBlockers(
	db *DB,
	result *ProviderRolloutReadiness,
	now time.Time,
) error {
	state, err := db.DWAcceptance.Query(KlineV2AcceptanceStream)
	if err != nil {
		return err
	}
	if state == nil || state.ContinuousSuccessStartedAt == nil {
		result.addBlocker("DW kline-v2 reconciliation has not started a continuous success window")
		return nil
	}
	readyAt := state.ContinuousSuccessStartedAt.UTC().Add(72 * time.Hour)
	if result.ReadinessNotBefore == nil || readyAt.After(*result.ReadinessNotBefore) {
		result.ReadinessNotBefore = &readyAt
	}
	if now.Before(readyAt) {
		result.addBlocker("DW kline-v2 reconciliation is incomplete until %s", readyAt.Format(time.RFC3339))
	}
	if state.ConsecutiveFailures > 0 {
		result.addBlocker("DW kline-v2 reconciliation has %d consecutive failures", state.ConsecutiveFailures)
	}
	if state.LastSuccessAt == nil || now.Sub(state.LastSuccessAt.UTC()) > 26*time.Hour {
		result.addBlocker("DW kline-v2 reconciliation has no success in the last 26 hours")
	}
	return nil
}

func readinessFeedPolicy(provider, mode string) (string, string, time.Duration, float64) {
	if mode == "shadow" || mode == "paused" {
		switch provider {
		case "binance", "coinbase", "bybit", "okx":
			return "spot-tickers-shadow", "venue_spot", 2 * time.Minute, 99
		}
	}
	switch provider {
	case "hyperliquid":
		return "metaAndAssetCtxs", "perp_mark", 2 * time.Minute, 99
	case "uniswap", "pancakeswap":
		return "route-quotes", "dex_route", time.Minute, 98
	default:
		return "spot-tickers", "venue_spot", 30 * time.Second, 99
	}
}

func readinessMinimumSoakHours(mode string) int {
	switch mode {
	case "canary":
		return 24
	case "enabled":
		return 48
	default:
		return 0
	}
}

func providerSuccessRate(attempts, successes int64) float64 {
	if attempts <= 0 {
		return 0
	}
	rate := float64(successes) / float64(attempts) * 100
	if rate > 100 {
		return 100
	}
	return rate
}

func providerSuccessRateText(attempts, successes int64) string {
	if attempts <= 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", providerSuccessRate(attempts, successes))
}
