package database

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderRolloutState struct {
	Provider              string         `gorm:"column:provider;primaryKey"`
	Mode                  string         `gorm:"column:mode"`
	RankLimit             int            `gorm:"column:rank_limit"`
	CanaryAssetIDs        datatypes.JSON `gorm:"column:canary_asset_ids"`
	LocalPreviewEnabled   bool           `gorm:"column:local_preview_enabled"`
	LocalPreviewEnabledAt *time.Time     `gorm:"column:local_preview_enabled_at"`
	MinSoakUntil          *time.Time     `gorm:"column:min_soak_until"`
	LastTransitionAt      time.Time      `gorm:"column:last_transition_at"`
	LastError             *string        `gorm:"column:last_error"`
	CreatedAt             time.Time      `gorm:"column:created_at"`
	UpdatedAt             time.Time      `gorm:"column:updated_at"`
}

func (ProviderRolloutState) TableName() string { return "provider_rollout_state" }

type ProviderAssetSelectionState struct {
	Provider         string    `gorm:"column:provider;primaryKey"`
	ActiveVersion    int64     `gorm:"column:active_version"`
	TargetCount      int       `gorm:"column:target_count"`
	CandidateCount   int       `gorm:"column:candidate_count"`
	SelectedCount    int       `gorm:"column:selected_count"`
	GeneratedAt      time.Time `gorm:"column:generated_at"`
	GenerationReason string    `gorm:"column:generation_reason"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (ProviderAssetSelectionState) TableName() string {
	return "provider_asset_selection_state"
}

type ProviderAssetSelection struct {
	Provider         string     `gorm:"column:provider;primaryKey"`
	SelectionVersion int64      `gorm:"column:selection_version;primaryKey"`
	AssetGuid        string     `gorm:"column:asset_guid;primaryKey"`
	SelectionRank    int        `gorm:"column:selection_rank"`
	MarketCapRank    *int       `gorm:"column:market_cap_rank"`
	SelectionReason  string     `gorm:"column:selection_reason"`
	SelectedAt       time.Time  `gorm:"column:selected_at"`
	ReplacedAt       *time.Time `gorm:"column:replaced_at"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
}

func (ProviderAssetSelection) TableName() string { return "provider_asset_selection" }

type AssetVenueSnapshot struct {
	AssetGuid          string         `gorm:"column:asset_guid;primaryKey"`
	Provider           string         `gorm:"column:provider;primaryKey"`
	PriceKind          string         `gorm:"column:price_kind;primaryKey"`
	PriceUSD           *string        `gorm:"column:price_usd"`
	Open24hUSD         *string        `gorm:"column:open_24h_usd"`
	Change24hPct       *string        `gorm:"column:change_24h_pct"`
	Turnover24hUSD     *string        `gorm:"column:turnover_24h_usd"`
	ContributorCount   int            `gorm:"column:contributor_count"`
	MarketCount        int            `gorm:"column:market_count"`
	Confidence         string         `gorm:"column:confidence"`
	Quality            string         `gorm:"column:quality"`
	Available          bool           `gorm:"column:available"`
	SourceTime         *time.Time     `gorm:"column:source_time"`
	ObservedAt         time.Time      `gorm:"column:observed_at"`
	LastAttemptAt      *time.Time     `gorm:"column:last_attempt_at"`
	LastSuccessAt      *time.Time     `gorm:"column:last_success_at"`
	AvailabilityStatus string         `gorm:"column:availability_status"`
	LastErrorClass     *string        `gorm:"column:last_error_class"`
	Version            int64          `gorm:"column:version"`
	Metadata           datatypes.JSON `gorm:"column:metadata"`
	CreatedAt          time.Time      `gorm:"column:created_at"`
	UpdatedAt          time.Time      `gorm:"column:updated_at"`
}

func (AssetVenueSnapshot) TableName() string { return "asset_venue_snapshot" }

type AssetRepresentation struct {
	AssetGuid          string     `gorm:"column:asset_guid"`
	ChainID            int64      `gorm:"column:chain_id;primaryKey"`
	ContractAddress    string     `gorm:"column:contract_address;primaryKey"`
	RepresentationKind string     `gorm:"column:representation_kind"`
	TokenSymbol        string     `gorm:"column:token_symbol"`
	Decimals           int        `gorm:"column:decimals"`
	ReviewStatus       string     `gorm:"column:review_status"`
	ReviewSource       *string    `gorm:"column:review_source"`
	ReviewNote         *string    `gorm:"column:review_note"`
	ReviewedAt         *time.Time `gorm:"column:reviewed_at"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
}

func (AssetRepresentation) TableName() string { return "asset_representation" }

type DexPoolCandidate struct {
	Provider         string         `gorm:"column:provider;primaryKey"`
	ChainID          int64          `gorm:"column:chain_id;primaryKey"`
	ProtocolVersion  string         `gorm:"column:protocol_version"`
	PoolAddress      string         `gorm:"column:pool_address;primaryKey"`
	Token0Address    string         `gorm:"column:token0_address"`
	Token1Address    string         `gorm:"column:token1_address"`
	FeeTier          int            `gorm:"column:fee_tier"`
	ResolutionStatus string         `gorm:"column:resolution_status"`
	RejectionReason  *string        `gorm:"column:rejection_reason"`
	QuoteEligible    bool           `gorm:"column:quote_eligible"`
	TVLUSD           *string        `gorm:"column:tvl_usd"`
	Volume24hUSD     *string        `gorm:"column:volume_24h_usd"`
	BlockNumber      *int64         `gorm:"column:block_number"`
	BlockTimestamp   *time.Time     `gorm:"column:block_timestamp"`
	FirstSeenAt      time.Time      `gorm:"column:first_seen_at"`
	LastSeenAt       time.Time      `gorm:"column:last_seen_at"`
	RawMetadata      datatypes.JSON `gorm:"column:raw_metadata"`
}

func (DexPoolCandidate) TableName() string { return "dex_pool_candidate" }

type DexRouteCurrent struct {
	Provider           string         `gorm:"column:provider;primaryKey"`
	AssetGuid          string         `gorm:"column:asset_guid;primaryKey"`
	RouteKey           string         `gorm:"column:route_key;primaryKey"`
	ChainID            int64          `gorm:"column:chain_id"`
	PriceUSD           *string        `gorm:"column:price_usd"`
	BuyPriceUSD        *string        `gorm:"column:buy_price_usd"`
	SellPriceUSD       *string        `gorm:"column:sell_price_usd"`
	Change24hPct       *string        `gorm:"column:change_24h_pct"`
	Turnover24hUSD     *string        `gorm:"column:turnover_24h_usd"`
	TVLUSD             *string        `gorm:"column:tvl_usd"`
	PriceImpactPct     *string        `gorm:"column:price_impact_pct"`
	RoundTripSpreadPct *string        `gorm:"column:round_trip_spread_pct"`
	QuoteNotionalUSD   string         `gorm:"column:quote_notional_usd"`
	QuoteReferenceKind string         `gorm:"column:quote_reference_kind"`
	BlockNumber        *int64         `gorm:"column:block_number"`
	BlockTimestamp     *time.Time     `gorm:"column:block_timestamp"`
	Quality            string         `gorm:"column:quality"`
	Available          bool           `gorm:"column:available"`
	Path               datatypes.JSON `gorm:"column:path"`
	PoolAddresses      datatypes.JSON `gorm:"column:pool_addresses"`
	ProtocolVersions   datatypes.JSON `gorm:"column:protocol_versions"`
	UnavailableReason  *string        `gorm:"column:unavailable_reason"`
	ObservedAt         time.Time      `gorm:"column:observed_at"`
	CreatedAt          time.Time      `gorm:"column:created_at"`
	UpdatedAt          time.Time      `gorm:"column:updated_at"`
}

func (DexRouteCurrent) TableName() string { return "dex_route_current" }

type DexQuoteObservation struct {
	Provider         string    `gorm:"column:provider;primaryKey"`
	AssetGuid        string    `gorm:"column:asset_guid;primaryKey"`
	RouteKey         string    `gorm:"column:route_key;primaryKey"`
	ObservedAt       time.Time `gorm:"column:observed_at;primaryKey"`
	PriceUSD         string    `gorm:"column:price_usd"`
	QuoteNotionalUSD string    `gorm:"column:quote_notional_usd"`
	BlockNumber      *int64    `gorm:"column:block_number"`
}

func (DexQuoteObservation) TableName() string { return "dex_quote_observation" }

type DexWindowCoverage struct {
	FirstObservedAt  time.Time     `gorm:"column:first_observed_at"`
	LastObservedAt   time.Time     `gorm:"column:last_observed_at"`
	OpenPriceUSD     string        `gorm:"column:open_price_usd"`
	ObservationCount int64         `gorm:"column:observation_count"`
	MaxGap           time.Duration `gorm:"-"`
	MaxGapSeconds    float64       `gorm:"column:max_gap_seconds"`
}

func (m *marketAggregationDB) QueryProviderRolloutStates() ([]ProviderRolloutState, error) {
	var rows []ProviderRolloutState
	err := m.gorm.Order("provider ASC").Find(&rows).Error
	return rows, err
}

func (m *marketAggregationDB) QueryProviderRollout(provider string) (*ProviderRolloutState, error) {
	var row ProviderRolloutState
	result := m.gorm.Where("provider = ?", normalizedProvider(provider)).First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, result.Error
}

func isCEXSelectionProvider(provider string) bool {
	switch normalizedProvider(provider) {
	case "binance", "coinbase", "bybit", "okx":
		return true
	default:
		return false
	}
}

func isDEXSelectionProvider(provider string) bool {
	switch normalizedProvider(provider) {
	case "hyperliquid", "uniswap", "pancakeswap":
		return true
	default:
		return false
	}
}

func providerSelectionUniverse() []string {
	return []string{
		"binance", "coinbase", "bybit", "okx",
		"hyperliquid", "uniswap", "pancakeswap",
	}
}

func normalizeSelectionProvider(provider string) (string, error) {
	provider = normalizedProvider(provider)
	if isCEXSelectionProvider(provider) || isDEXSelectionProvider(provider) {
		return provider, nil
	}
	return "", fmt.Errorf("provider %s does not support an asset selection", provider)
}

type providerSelectionCandidate struct {
	AssetGuid     string `gorm:"column:asset_guid"`
	MarketCapRank *int   `gorm:"column:market_cap_rank"`
}

func queryProviderSelectionCandidates(
	tx *gorm.DB,
	provider string,
) ([]providerSelectionCandidate, error) {
	var candidates []providerSelectionCandidate
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
		err := tx.Table("provider_market_candidate candidate").
			Select(`candidate.base_asset_guid AS asset_guid,
				MIN(metric.market_cap_rank) AS market_cap_rank`).
			Joins("JOIN asset_metric_current metric ON metric.asset_guid = candidate.base_asset_guid").
			Where(`candidate.provider = ?
				AND candidate.market_type = 'spot'
				AND candidate.resolution_status IN ('resolved', 'enabled')
				AND candidate.base_asset_guid IS NOT NULL
				AND metric.market_cap_rank BETWEEN 1 AND 200`,
				provider).
			Group("candidate.base_asset_guid").
			Order("MIN(metric.market_cap_rank) ASC, candidate.base_asset_guid ASC").
			Scan(&candidates).Error
		return candidates, err
	case "hyperliquid":
		err := tx.Table("provider_market_candidate candidate").
			Select(`candidate.base_asset_guid AS asset_guid,
				MIN(metric.market_cap_rank) AS market_cap_rank`).
			Joins("JOIN asset_metric_current metric ON metric.asset_guid = candidate.base_asset_guid").
			Where(`candidate.provider = 'hyperliquid'
				AND candidate.market_type = 'perp'
				AND candidate.resolution_status IN ('resolved', 'enabled')
				AND candidate.base_asset_guid IS NOT NULL
				AND metric.market_cap_rank BETWEEN 1 AND 200`).
			Group("candidate.base_asset_guid").
			Order("MIN(metric.market_cap_rank) ASC, candidate.base_asset_guid ASC").
			Scan(&candidates).Error
		return candidates, err
	case "uniswap", "pancakeswap":
		chainID := int64(1)
		if provider == "pancakeswap" {
			chainID = 56
		}
		err := tx.Table("asset_representation representation").
			Select(`representation.asset_guid AS asset_guid,
				MIN(metric.market_cap_rank) AS market_cap_rank`).
			Joins("JOIN asset_metric_current metric ON metric.asset_guid = representation.asset_guid").
			Joins(`JOIN dex_pool_candidate pool
				ON pool.provider = ?
				AND pool.chain_id = representation.chain_id
				AND pool.resolution_status IN ('resolved', 'enabled')
				AND (
					pool.token0_address = representation.contract_address
					OR pool.token1_address = representation.contract_address
				)`, provider).
			Where(`representation.chain_id = ?
				AND representation.review_status = 'approved'
				AND metric.market_cap_rank BETWEEN 1 AND 200`,
				chainID).
			Group("representation.asset_guid").
			Order("MIN(metric.market_cap_rank) ASC, representation.asset_guid ASC").
			Scan(&candidates).Error
		return candidates, err
	default:
		return nil, fmt.Errorf("provider %s does not support an asset selection", provider)
	}
}

func (m *marketAggregationDB) QueryProviderAssetSelectionState(
	provider string,
) (*ProviderAssetSelectionState, error) {
	provider, err := normalizeSelectionProvider(provider)
	if err != nil {
		return nil, err
	}
	var row ProviderAssetSelectionState
	result := m.gorm.Where("provider = ?", provider).First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, result.Error
}

func (m *marketAggregationDB) QueryProviderAssetSelection(
	provider string,
) ([]ProviderAssetSelection, *ProviderAssetSelectionState, error) {
	state, err := m.QueryProviderAssetSelectionState(provider)
	if err != nil || state == nil {
		return nil, state, err
	}
	var rows []ProviderAssetSelection
	err = m.gorm.Where(
		"provider = ? AND selection_version = ?", state.Provider, state.ActiveVersion,
	).Order("selection_rank ASC").Find(&rows).Error
	return rows, state, err
}

// RefreshProviderAssetSelection builds a new provider-local universe from the
// reviewed Top-200 catalog and atomically changes the active version. The
// previous version remains queryable for audit and rollback.
func (m *marketAggregationDB) RefreshProviderAssetSelection(
	provider string,
	targetCount int,
	reason string,
) (*ProviderAssetSelectionState, error) {
	provider, err := normalizeSelectionProvider(provider)
	if err != nil {
		return nil, err
	}
	if targetCount < 1 || targetCount > 200 {
		return nil, fmt.Errorf("selection target must be between 1 and 200")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("selection reason is required")
	}
	var selectedState *ProviderAssetSelectionState
	err = m.gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "provider_asset_selection:"+provider).Error; err != nil {
			return err
		}
		candidates, err := queryProviderSelectionCandidates(tx, provider)
		if err != nil {
			return err
		}
		if len(candidates) < targetCount {
			return fmt.Errorf(
				"provider %s has only %d identity-verified Top-200 assets; selection target is %d",
				provider, len(candidates), targetCount,
			)
		}
		selectedCount := targetCount
		var existingState ProviderAssetSelectionState
		existingResult := tx.Where("provider = ?", provider).First(&existingState)
		if existingResult.Error != nil && existingResult.Error != gorm.ErrRecordNotFound {
			return existingResult.Error
		}
		if existingResult.Error == nil && existingState.TargetCount == targetCount {
			var existing []ProviderAssetSelection
			if err := tx.Where(
				"provider = ? AND selection_version = ?",
				provider, existingState.ActiveVersion,
			).Order("selection_rank ASC").Find(&existing).Error; err != nil {
				return err
			}
			unchanged := len(existing) == selectedCount
			for index := 0; unchanged && index < selectedCount; index++ {
				unchanged = existing[index].AssetGuid == candidates[index].AssetGuid
			}
			if unchanged {
				now := time.Now().UTC()
				if err := tx.Model(&ProviderAssetSelectionState{}).
					Where("provider = ?", provider).
					Updates(map[string]interface{}{
						"candidate_count":   len(candidates),
						"generation_reason": reason,
						"updated_at":        now,
					}).Error; err != nil {
					return err
				}
				existingState.CandidateCount = len(candidates)
				existingState.GenerationReason = reason
				existingState.UpdatedAt = now
				selectedState = &existingState
				return nil
			}
		}
		var currentVersion int64
		if err := tx.Table("provider_asset_selection_state").
			Select("COALESCE(MAX(active_version), 0)").
			Where("provider = ?", provider).
			Scan(&currentVersion).Error; err != nil {
			return err
		}
		version := currentVersion + 1
		now := time.Now().UTC()
		if existingResult.Error == nil {
			if err := tx.Table("provider_asset_selection").
				Where("provider = ? AND selection_version = ? AND replaced_at IS NULL",
					provider, existingState.ActiveVersion).
				Update("replaced_at", now).Error; err != nil {
				return err
			}
		}
		rows := make([]ProviderAssetSelection, 0, selectedCount)
		for index, item := range candidates[:selectedCount] {
			rows = append(rows, ProviderAssetSelection{
				Provider: provider, SelectionVersion: version,
				AssetGuid: item.AssetGuid, SelectionRank: index + 1,
				MarketCapRank: item.MarketCapRank, SelectionReason: reason,
				SelectedAt: now, CreatedAt: now,
			})
		}
		if len(rows) > 0 {
			if err := tx.CreateInBatches(rows, 100).Error; err != nil {
				return err
			}
		}
		state := ProviderAssetSelectionState{
			Provider: provider, ActiveVersion: version,
			TargetCount: targetCount, CandidateCount: len(candidates),
			SelectedCount: len(rows), GeneratedAt: now,
			GenerationReason: reason, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"active_version", "target_count", "candidate_count",
				"selected_count", "generated_at", "generation_reason", "updated_at",
			}),
		}).Create(&state).Error; err != nil {
			return err
		}
		selectedState = &state
		return nil
	})
	return selectedState, err
}

func (m *marketAggregationDB) QueryProviderSelectedAssetIDs(
	provider string,
) (map[string]struct{}, *ProviderAssetSelectionState, error) {
	rows, state, err := m.QueryProviderAssetSelection(provider)
	if err != nil || state == nil {
		return map[string]struct{}{}, state, err
	}
	result := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		result[row.AssetGuid] = struct{}{}
	}
	return result, state, nil
}

// EnsureProviderAssetSelection creates or repairs a selection, but never
// replaces a complete selection merely because market-cap ranks changed. A new
// version is generated only when the target changes or a selected asset is no
// longer a reviewed, tradable Top-200 candidate.
func (m *marketAggregationDB) EnsureProviderAssetSelection(
	provider string,
	targetCount int,
	reason string,
) (*ProviderAssetSelectionState, error) {
	selected, state, err := m.QueryProviderSelectedAssetIDs(provider)
	if err != nil {
		return nil, err
	}
	eligible, eligibleErr := m.QueryEligibleProviderAssetIDs(provider, 200)
	if eligibleErr != nil {
		return nil, eligibleErr
	}
	expectedCount := targetCount
	if state != nil && state.TargetCount == targetCount && len(selected) == expectedCount {
		eligibleSet := make(map[string]struct{}, len(eligible))
		for _, assetID := range eligible {
			eligibleSet[assetID] = struct{}{}
		}
		valid := true
		for assetID := range selected {
			if _, exists := eligibleSet[assetID]; !exists {
				valid = false
				break
			}
		}
		if valid {
			return state, nil
		}
	}
	return m.RefreshProviderAssetSelection(provider, targetCount, reason)
}

func (m *marketAggregationDB) QuerySelectedAssetUnionIDs() (map[string]struct{}, error) {
	var ids []string
	err := m.gorm.Table("provider_asset_selection selection").
		Select("selection.asset_guid").
		Joins(`JOIN provider_asset_selection_state state
			ON state.provider = selection.provider
			AND state.active_version = selection.selection_version`).
		Where("selection.provider IN ?", providerSelectionUniverse()).
		Group("selection.asset_guid").
		Order("selection.asset_guid ASC").
		Pluck("selection.asset_guid", &ids).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result, nil
}

func (m *marketAggregationDB) SetProviderRollout(
	provider, mode string,
	rankLimit int,
	canaryAssetIDs []string,
	minSoakUntil *time.Time,
) error {
	provider = normalizedProvider(provider)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if provider == "" {
		return fmt.Errorf("provider is required")
	}
	switch mode {
	case "shadow", "canary", "enabled", "paused":
	default:
		return fmt.Errorf("unsupported rollout mode %q", mode)
	}
	if rankLimit < 1 || rankLimit > 200 {
		return fmt.Errorf("rank limit must be between 1 and 200")
	}
	canaryAssetIDs = normalizeRolloutAssetIDs(canaryAssetIDs)
	if mode == "canary" {
		if len(canaryAssetIDs) == 0 {
			eligible, err := m.QueryEligibleProviderAssetIDs(provider, rankLimit)
			if err != nil {
				return err
			}
			if len(eligible) < 10 {
				return fmt.Errorf(
					"provider %s has only %d reviewed eligible assets; canary requires 10",
					provider, len(eligible),
				)
			}
			canaryAssetIDs = append([]string(nil), eligible[:10]...)
		}
		if len(canaryAssetIDs) != 10 {
			return fmt.Errorf("provider %s canary must contain exactly 10 unique assets", provider)
		}
	}
	raw, err := json.Marshal(canaryAssetIDs)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	row := ProviderRolloutState{
		Provider: provider, Mode: mode, RankLimit: rankLimit,
		CanaryAssetIDs: raw, MinSoakUntil: minSoakUntil,
		LastTransitionAt: now, UpdatedAt: now,
	}
	return m.gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"mode": mode, "rank_limit": rankLimit, "canary_asset_ids": raw,
				"min_soak_until": minSoakUntil, "last_transition_at": now,
				"last_error": nil, "updated_at": now,
			}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return tx.Table("market_provider_status").
			Where("provider = ?", provider).
			Updates(map[string]interface{}{
				"window_started_at":      now,
				"observation_started_at": nil,
				"attempt_count":          0,
				"success_count":          0,
				"consecutive_failures":   0,
				"next_retry_at":          nil,
				"last_error_class":       nil,
				"last_error_summary":     nil,
				"updated_at":             now,
			}).Error
	})
}

// SetProviderLocalPreview changes only the local preview boundary. Formal
// rollout mode, frozen canary assets, transition timestamps, and soak dates
// remain untouched. Disabling preview invalidates preview-fed venue snapshots
// and resets formal observation counters so local development can never count
// toward a production promotion.
func (m *marketAggregationDB) SetProviderLocalPreview(provider string, enabled bool) error {
	provider = normalizedProvider(provider)
	if !isCEXSelectionProvider(provider) && !isDEXSelectionProvider(provider) {
		return fmt.Errorf("provider %s does not support local preview", provider)
	}
	priceKind, formalSourceKey, previewSourceKey := providerPreviewKeys(provider)
	now := time.Now().UTC()
	current, err := m.QueryProviderRollout(provider)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("provider %s rollout is not configured", provider)
	}
	if current.LocalPreviewEnabled == enabled {
		return nil
	}
	var enabledAt *time.Time
	if enabled {
		enabledAt = &now
	}
	return m.gorm.Transaction(func(tx *gorm.DB) error {
		result := tx.Table("provider_rollout_state").
			Where("provider = ?", provider).
			Updates(map[string]interface{}{
				"local_preview_enabled":    enabled,
				"local_preview_enabled_at": enabledAt,
				"updated_at":               now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("provider %s rollout is not configured", provider)
		}
		if enabled {
			return nil
		}
		if err := tx.Table("asset_venue_snapshot").
			Where("provider = ? AND price_kind = ?", provider, priceKind).
			Updates(map[string]interface{}{
				"available":           false,
				"last_success_at":     nil,
				"availability_status": "unavailable",
				"last_error_class":    "preview_disabled",
				"updated_at":          now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Where(
			"provider = ? AND source_key = ?", provider, previewSourceKey,
		).Delete(&ProviderStatus{}).Error; err != nil {
			return err
		}
		return tx.Table("market_provider_status").
			Where("provider = ? AND source_key = ?", provider, formalSourceKey).
			Updates(map[string]interface{}{
				"last_attempt_at":        nil,
				"last_success_at":        nil,
				"next_retry_at":          nil,
				"window_started_at":      now,
				"observation_started_at": nil,
				"attempt_count":          0,
				"success_count":          0,
				"consecutive_failures":   0,
				"last_source_time":       nil,
				"last_error_class":       nil,
				"last_error_summary":     nil,
				"details":                datatypes.JSON([]byte(`{}`)),
				"updated_at":             now,
			}).Error
	})
}

func providerPreviewKeys(provider string) (priceKind, formalSourceKey, previewSourceKey string) {
	switch normalizedProvider(provider) {
	case "hyperliquid":
		return "perp_mark", "metaAndAssetCtxs", "metaAndAssetCtxs-preview"
	case "uniswap", "pancakeswap":
		return "dex_route", "route-quotes", "route-quotes-preview"
	default:
		return "venue_spot", "spot-tickers", "spot-tickers-preview"
	}
}

func (m *marketAggregationDB) QueryEligibleProviderAssetIDs(
	provider string,
	rankLimit int,
) ([]string, error) {
	provider = normalizedProvider(provider)
	if rankLimit < 1 || rankLimit > 200 {
		return nil, fmt.Errorf("rank limit must be between 1 and 200")
	}
	if provider == "uniswap" || provider == "pancakeswap" {
		chainID := int64(1)
		if provider == "pancakeswap" {
			chainID = 56
		}
		var ids []string
		err := m.gorm.Table("asset_representation representation").
			Select("representation.asset_guid").
			Joins("JOIN asset_metric_current metric ON metric.asset_guid = representation.asset_guid").
			Joins(`JOIN dex_pool_candidate pool
				ON pool.provider = ?
				AND pool.chain_id = representation.chain_id
				AND pool.resolution_status IN ('resolved', 'enabled')
				AND (
					pool.token0_address = representation.contract_address
					OR pool.token1_address = representation.contract_address
				)`, provider).
			Where(`representation.chain_id = ?
				AND representation.review_status = 'approved'
				AND metric.market_cap_rank BETWEEN 1 AND ?`,
				chainID, rankLimit).
			Group("representation.asset_guid").
			Order("MIN(metric.market_cap_rank) ASC, representation.asset_guid ASC").
			Pluck("representation.asset_guid", &ids).Error
		return ids, err
	}
	marketType := "spot"
	if provider == "hyperliquid" {
		marketType = "perp"
	}
	var ids []string
	err := m.gorm.Table("provider_market_candidate candidate").
		Select("candidate.base_asset_guid").
		Joins("JOIN asset_metric_current metric ON metric.asset_guid = candidate.base_asset_guid").
		Where(`candidate.provider = ?
			AND candidate.market_type = ?
			AND candidate.resolution_status IN ('resolved', 'enabled')
			AND metric.market_cap_rank BETWEEN 1 AND ?`,
			provider, marketType, rankLimit).
		Group("candidate.base_asset_guid").
		Order("MIN(metric.market_cap_rank) ASC, candidate.base_asset_guid ASC").
		Pluck("candidate.base_asset_guid", &ids).Error
	return ids, err
}

func (m *marketAggregationDB) ensureFixedCanaryAssetIDs(
	state *ProviderRolloutState,
) (*ProviderRolloutState, error) {
	if state == nil || state.Mode != "canary" {
		return state, nil
	}
	explicit, err := decodeRolloutAssetIDs(state.CanaryAssetIDs)
	if err != nil {
		return nil, fmt.Errorf("provider %s has invalid canary_asset_ids: %w", state.Provider, err)
	}
	if len(explicit) > 0 {
		return state, nil
	}
	eligible, err := m.QueryEligibleProviderAssetIDs(state.Provider, state.RankLimit)
	if err != nil {
		return nil, err
	}
	if len(eligible) < 10 {
		return nil, fmt.Errorf(
			"provider %s canary is not frozen and only %d reviewed assets are eligible; need 10",
			state.Provider, len(eligible),
		)
	}
	fixed := append([]string(nil), eligible[:10]...)
	raw, err := json.Marshal(fixed)
	if err != nil {
		return nil, err
	}
	result := m.gorm.Table("provider_rollout_state").
		Where("provider = ? AND mode = 'canary' AND canary_asset_ids = '[]'::jsonb", state.Provider).
		Updates(map[string]interface{}{
			"canary_asset_ids": raw,
			"updated_at":       time.Now().UTC(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	// Another supervisor may have frozen the same deterministic set first.
	// Always re-read the row so callers publish the persisted boundary.
	return m.QueryProviderRollout(state.Provider)
}

// QueryRolloutAssetIDs resolves the exact set a provider may expose to the
// formal snapshot writer. Shadow/paused providers always return an empty set.
// A legacy canary with no explicit list is frozen once to the first ten
// eligible assets. Later catalog/rank changes can never silently replace that
// persisted canary set.
func (m *marketAggregationDB) QueryRolloutAssetIDs(provider string) (map[string]struct{}, *ProviderRolloutState, error) {
	state, err := m.QueryProviderRollout(provider)
	if err != nil || state == nil {
		return map[string]struct{}{}, state, err
	}
	result := make(map[string]struct{})
	if state.Mode == "shadow" || state.Mode == "paused" {
		return result, state, nil
	}
	state, err = m.ensureFixedCanaryAssetIDs(state)
	if err != nil {
		return nil, state, err
	}
	explicit, err := decodeRolloutAssetIDs(state.CanaryAssetIDs)
	if err != nil {
		return nil, state, fmt.Errorf("provider %s has invalid canary_asset_ids: %w", provider, err)
	}
	eligible, err := m.QueryEligibleProviderAssetIDs(provider, state.RankLimit)
	if err != nil {
		return nil, state, err
	}
	selected := eligible
	if state.Mode == "canary" {
		eligibleSet := make(map[string]struct{}, len(eligible))
		for _, id := range eligible {
			eligibleSet[id] = struct{}{}
		}
		missing := make([]string, 0)
		for _, id := range explicit {
			if _, ok := eligibleSet[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, state, fmt.Errorf(
				"provider %s fixed canary assets are no longer eligible: %s",
				provider, strings.Join(missing, ","),
			)
		}
		selected = explicit
	}
	for _, id := range selected {
		result[id] = struct{}{}
	}
	return result, state, nil
}

// QueryPublishedAssetIDs returns the effective local read/write boundary.
// Preview uses the active, versioned provider selection while formal rollout
// keeps using the persisted shadow/canary/enabled policy.
func (m *marketAggregationDB) QueryPublishedAssetIDs(provider string) (map[string]struct{}, *ProviderRolloutState, error) {
	state, err := m.QueryProviderRollout(provider)
	if err != nil || state == nil || !state.LocalPreviewEnabled {
		if err != nil || state == nil {
			return map[string]struct{}{}, state, err
		}
		return m.QueryRolloutAssetIDs(provider)
	}
	selected, selectionState, err := m.QueryProviderSelectedAssetIDs(provider)
	if err != nil {
		return nil, state, err
	}
	if selectionState == nil {
		return nil, state, fmt.Errorf(
			"provider %s local preview has no active asset selection", provider,
		)
	}
	return selected, state, nil
}

// ReconcileResolvedSpotMarkets applies the persisted rollout boundary to the
// last successfully stored catalog. It does no network I/O, so rollout changes
// take effect immediately instead of waiting for the six-hour discovery pass.
func (m *marketAggregationDB) ReconcileResolvedSpotMarkets(provider string) (int64, error) {
	provider = normalizedProvider(provider)
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
	default:
		return 0, fmt.Errorf("provider %s does not use the CEX spot catalog", provider)
	}
	allowed, rollout, err := m.QueryPublishedAssetIDs(provider)
	if err != nil {
		return 0, err
	}
	if rollout == nil {
		return 0, nil
	}
	var latest struct {
		LastSeenAt time.Time `gorm:"column:last_seen_at"`
	}
	result := m.gorm.Table("provider_market_candidate").
		Select("last_seen_at").
		Where("provider = ? AND market_type = 'spot'", provider).
		Order("last_seen_at DESC").
		Limit(1).
		Scan(&latest)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 || latest.LastSeenAt.IsZero() {
		return 0, nil
	}
	return m.EnableResolvedSpotMarkets(provider, latest.LastSeenAt.UTC(), allowed)
}

func decodeRolloutAssetIDs(raw datatypes.JSON) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return normalizeRolloutAssetIDs(values), nil
}

func normalizeRolloutAssetIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (m *marketAggregationDB) QueryFreshVenueAssetIDs(
	provider, priceKind string,
	since time.Time,
) ([]string, error) {
	var ids []string
	err := m.gorm.Table("asset_venue_snapshot").
		Where(`provider = ? AND price_kind = ? AND available = TRUE
			AND observed_at >= ?`,
			normalizedProvider(provider), strings.TrimSpace(priceKind), since.UTC()).
		Order("asset_guid ASC").
		Pluck("asset_guid", &ids).Error
	return ids, err
}

func (m *marketAggregationDB) QueryFreshVenueAssetEvidence(
	provider, priceKind string,
	since time.Time,
) ([]VenueAssetEvidence, error) {
	var rows []VenueAssetEvidence
	err := m.gorm.Table("asset_venue_snapshot").
		Select(`asset_guid AS asset_id,
			(change_24h_pct IS NOT NULL) AS has_change`).
		Where(`provider = ? AND price_kind = ? AND available = TRUE
			AND observed_at >= ?`,
			normalizedProvider(provider), strings.TrimSpace(priceKind), since.UTC()).
		Order("asset_guid ASC").
		Scan(&rows).Error
	return rows, err
}

// ApplyReviewedAssetAliases applies a code-reviewed manifest. It may repair
// aliases that were previously stamped by the legacy migration, but refuses
// to overwrite a conflicting human-reviewed identity.
func (m *marketAggregationDB) ApplyReviewedAssetAliases(items []AssetAlias) error {
	return m.gorm.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			item.Provider = normalizedProvider(item.Provider)
			item.Alias = strings.ToUpper(strings.TrimSpace(item.Alias))
			var existing AssetAlias
			result := tx.Where("provider = ? AND alias = ?", item.Provider, item.Alias).
				Limit(1).Find(&existing)
			if result.Error != nil {
				return result.Error
			}
			exists := result.RowsAffected > 0
			sameReviewedSource := exists &&
				existing.ReviewedBy != nil && item.ReviewedBy != nil &&
				*existing.ReviewedBy == *item.ReviewedBy &&
				existing.ReviewSource != nil && item.ReviewSource != nil &&
				*existing.ReviewSource == *item.ReviewSource
			legacyBootstrap := exists && existing.ReviewedBy != nil &&
				*existing.ReviewedBy == "migration-existing-catalog"
			if exists && existing.ReviewStatus == "approved" &&
				existing.AssetGuid != item.AssetGuid &&
				!legacyBootstrap && !sameReviewedSource {
				return fmt.Errorf(
					"reviewed alias conflict %s:%s existing=%s requested=%s",
					item.Provider, item.Alias, existing.AssetGuid, item.AssetGuid,
				)
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "provider"}, {Name: "alias"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"asset_guid", "review_status", "reviewed_by", "reviewed_at",
					"review_source", "review_note", "updated_at",
				}),
			}).Create(&item).Error; err != nil {
				return err
			}
			// The legacy Hyperliquid bootstrap created provider-local assets.
			// Rebind only its exact source-symbol market to the reviewed
			// canonical identity; the old asset row remains for audit/rollback.
			if item.Provider == "hyperliquid" {
				if err := tx.Exec(`
					UPDATE symbol symbol_row
					SET base_asset_guid = ?, updated_at = clock_timestamp()
					FROM exchange_symbol market
					JOIN exchange venue ON venue.guid = market.exchange_guid
					WHERE symbol_row.guid = market.symbol_guid
					  AND venue.code = 'hyperliquid'
					  AND upper(market.source_symbol) = ?
					  AND lower(symbol_row.market_type) = 'perp'`,
					item.AssetGuid, item.Alias,
				).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (m *marketAggregationDB) UpsertAssetRepresentations(items []AssetRepresentation) error {
	if len(items) == 0 {
		return nil
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chain_id"}, {Name: "contract_address"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"asset_guid", "representation_kind", "token_symbol", "decimals",
			"review_status", "review_source", "review_note", "reviewed_at", "updated_at",
		}),
	}).CreateInBatches(items, 100).Error
}

func (m *marketAggregationDB) QueryApprovedAssetRepresentations(chainID int64) ([]AssetRepresentation, error) {
	var rows []AssetRepresentation
	query := m.gorm.Where("review_status = 'approved'")
	if chainID > 0 {
		query = query.Where("chain_id = ?", chainID)
	}
	err := query.Order("chain_id ASC, asset_guid ASC, contract_address ASC").Find(&rows).Error
	return rows, err
}

func (m *marketAggregationDB) UpsertAssetVenueSnapshots(items []AssetVenueSnapshot) error {
	if len(items) == 0 {
		return nil
	}
	prepareAssetVenueSnapshots(items)
	materialChange := assetVenueSnapshotMaterialChange()
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "asset_guid"}, {Name: "provider"}, {Name: "price_kind"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"price_usd":           gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.price_usd ELSE asset_venue_snapshot.price_usd END"),
			"open_24h_usd":        gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.open_24h_usd ELSE asset_venue_snapshot.open_24h_usd END"),
			"change_24h_pct":      gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.change_24h_pct ELSE asset_venue_snapshot.change_24h_pct END"),
			"turnover_24h_usd":    gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.turnover_24h_usd ELSE asset_venue_snapshot.turnover_24h_usd END"),
			"contributor_count":   gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.contributor_count ELSE asset_venue_snapshot.contributor_count END"),
			"market_count":        gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.market_count ELSE asset_venue_snapshot.market_count END"),
			"confidence":          gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.confidence ELSE asset_venue_snapshot.confidence END"),
			"quality":             gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.quality ELSE asset_venue_snapshot.quality END"),
			"available":           gorm.Expr("asset_venue_snapshot.available OR EXCLUDED.available"),
			"source_time":         gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.source_time ELSE asset_venue_snapshot.source_time END"),
			"observed_at":         gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.observed_at ELSE asset_venue_snapshot.observed_at END"),
			"last_attempt_at":     gorm.Expr("EXCLUDED.last_attempt_at"),
			"last_success_at":     gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.last_success_at ELSE asset_venue_snapshot.last_success_at END"),
			"availability_status": gorm.Expr("CASE WHEN EXCLUDED.available THEN 'fresh' ELSE 'unavailable' END"),
			"last_error_class":    gorm.Expr("CASE WHEN EXCLUDED.available THEN NULL ELSE EXCLUDED.last_error_class END"),
			"version":             gorm.Expr("CASE WHEN EXCLUDED.available AND (" + materialChange + ") THEN nextval('asset_venue_snapshot_version_seq') ELSE asset_venue_snapshot.version END"),
			"metadata":            gorm.Expr("EXCLUDED.metadata"),
			"updated_at":          gorm.Expr("CASE WHEN EXCLUDED.available AND (" + materialChange + ") THEN clock_timestamp() ELSE asset_venue_snapshot.updated_at END"),
		}),
	}).CreateInBatches(items, 250).Error
}

// ReplaceDexVenueSnapshots publishes the currently executable DEX route set.
//
// Generic venue snapshots preserve the last successful value when a provider
// temporarily fails. A DEX route is different: pool discovery can replace the
// selected route, so retaining an old route as available would advertise a
// quote that can no longer be reproduced. This write path therefore treats the
// current route evaluation as authoritative and permits available=true to
// become available=false while preserving last_success_at as audit evidence.
func (m *marketAggregationDB) ReplaceDexVenueSnapshots(items []AssetVenueSnapshot) error {
	if len(items) == 0 {
		return nil
	}
	provider := ""
	assetIDs := make([]string, 0, len(items))
	attemptedAt := items[0].ObservedAt.UTC()
	for index := range items {
		itemProvider := normalizedProvider(items[index].Provider)
		if (itemProvider != "uniswap" && itemProvider != "pancakeswap") ||
			items[index].PriceKind != "dex_route" {
			return fmt.Errorf(
				"authoritative DEX snapshot requires uniswap/pancakeswap dex_route, got %s/%s",
				items[index].Provider, items[index].PriceKind,
			)
		}
		if provider == "" {
			provider = itemProvider
		} else if provider != itemProvider {
			return fmt.Errorf("authoritative DEX snapshot batch cannot mix providers")
		}
		items[index].Provider = itemProvider
		assetIDs = append(assetIDs, items[index].AssetGuid)
		if items[index].ObservedAt.After(attemptedAt) {
			attemptedAt = items[index].ObservedAt.UTC()
		}
		if !items[index].Available {
			items[index].PriceUSD = nil
			items[index].Open24hUSD = nil
			items[index].Change24hPct = nil
			items[index].Turnover24hUSD = nil
			items[index].ContributorCount = 0
		}
	}
	prepareAssetVenueSnapshots(items)
	materialChange := assetVenueSnapshotMaterialChange()
	return m.gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "asset_guid"}, {Name: "provider"}, {Name: "price_kind"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"price_usd":           gorm.Expr("EXCLUDED.price_usd"),
				"open_24h_usd":        gorm.Expr("EXCLUDED.open_24h_usd"),
				"change_24h_pct":      gorm.Expr("EXCLUDED.change_24h_pct"),
				"turnover_24h_usd":    gorm.Expr("EXCLUDED.turnover_24h_usd"),
				"contributor_count":   gorm.Expr("EXCLUDED.contributor_count"),
				"market_count":        gorm.Expr("EXCLUDED.market_count"),
				"confidence":          gorm.Expr("EXCLUDED.confidence"),
				"quality":             gorm.Expr("EXCLUDED.quality"),
				"available":           gorm.Expr("EXCLUDED.available"),
				"source_time":         gorm.Expr("EXCLUDED.source_time"),
				"observed_at":         gorm.Expr("EXCLUDED.observed_at"),
				"last_attempt_at":     gorm.Expr("EXCLUDED.last_attempt_at"),
				"last_success_at":     gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.last_success_at ELSE asset_venue_snapshot.last_success_at END"),
				"availability_status": gorm.Expr("EXCLUDED.availability_status"),
				"last_error_class":    gorm.Expr("EXCLUDED.last_error_class"),
				"version":             gorm.Expr("CASE WHEN " + materialChange + " THEN nextval('asset_venue_snapshot_version_seq') ELSE asset_venue_snapshot.version END"),
				"metadata":            gorm.Expr("EXCLUDED.metadata"),
				"updated_at":          gorm.Expr("CASE WHEN " + materialChange + " THEN clock_timestamp() ELSE asset_venue_snapshot.updated_at END"),
			}),
		}).CreateInBatches(items, 250).Error; err != nil {
			return err
		}
		return tx.Model(&AssetVenueSnapshot{}).
			Where("provider = ? AND price_kind = 'dex_route'", provider).
			Where("asset_guid NOT IN ?", assetIDs).
			Updates(map[string]interface{}{
				"price_usd":           nil,
				"open_24h_usd":        nil,
				"change_24h_pct":      nil,
				"turnover_24h_usd":    nil,
				"contributor_count":   0,
				"available":           false,
				"observed_at":         attemptedAt,
				"last_attempt_at":     attemptedAt,
				"availability_status": "unavailable",
				"last_error_class":    "selection_not_current",
				"version": gorm.Expr(
					"CASE WHEN available OR price_usd IS NOT NULL THEN nextval('asset_venue_snapshot_version_seq') ELSE version END",
				),
				"metadata": gorm.Expr(
					`COALESCE(metadata, '{}'::jsonb) ||
						jsonb_build_object('exclusions',
							jsonb_build_array(jsonb_build_object('reason', 'selection_not_current')))`,
				),
				"updated_at": gorm.Expr(
					"CASE WHEN available OR price_usd IS NOT NULL THEN clock_timestamp() ELSE updated_at END",
				),
			}).Error
	})
}

func prepareAssetVenueSnapshots(items []AssetVenueSnapshot) {
	for index := range items {
		if len(items[index].Metadata) == 0 {
			items[index].Metadata = datatypes.JSON([]byte(`{}`))
		}
		if strings.TrimSpace(items[index].Confidence) == "" {
			items[index].Confidence = "unknown"
		}
		if strings.TrimSpace(items[index].Quality) == "" {
			items[index].Quality = "unknown"
		}
		attemptedAt := items[index].ObservedAt.UTC()
		items[index].LastAttemptAt = &attemptedAt
		if items[index].Available {
			items[index].LastSuccessAt = &attemptedAt
			items[index].AvailabilityStatus = "fresh"
			items[index].LastErrorClass = nil
		} else {
			items[index].AvailabilityStatus = "unavailable"
			if items[index].LastErrorClass == nil {
				value := snapshotUnavailableReason(items[index].Metadata)
				items[index].LastErrorClass = &value
			}
		}
	}
}

func assetVenueSnapshotMaterialChange() string {
	return `ROW(
		asset_venue_snapshot.price_usd,
		asset_venue_snapshot.open_24h_usd,
		asset_venue_snapshot.change_24h_pct,
		asset_venue_snapshot.turnover_24h_usd,
		asset_venue_snapshot.contributor_count,
		asset_venue_snapshot.market_count,
		asset_venue_snapshot.confidence,
		asset_venue_snapshot.quality,
		asset_venue_snapshot.available,
		asset_venue_snapshot.source_time,
		asset_venue_snapshot.metadata
	) IS DISTINCT FROM ROW(
		EXCLUDED.price_usd,
		EXCLUDED.open_24h_usd,
		EXCLUDED.change_24h_pct,
		EXCLUDED.turnover_24h_usd,
		EXCLUDED.contributor_count,
		EXCLUDED.market_count,
		EXCLUDED.confidence,
		EXCLUDED.quality,
		EXCLUDED.available,
		EXCLUDED.source_time,
		EXCLUDED.metadata
	)`
}

func snapshotUnavailableReason(metadata datatypes.JSON) string {
	var value struct {
		Exclusions []struct {
			Reason string `json:"reason"`
		} `json:"exclusions"`
	}
	if json.Unmarshal(metadata, &value) == nil && len(value.Exclusions) > 0 {
		reason := strings.TrimSpace(value.Exclusions[0].Reason)
		if reason != "" {
			return reason
		}
	}
	return "source_unavailable"
}

func (m *marketAggregationDB) UpsertDexPoolCandidates(items []DexPoolCandidate) error {
	if len(items) == 0 {
		return nil
	}
	for index := range items {
		if len(items[index].RawMetadata) == 0 {
			items[index].RawMetadata = datatypes.JSON([]byte(`{}`))
		}
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider"}, {Name: "chain_id"}, {Name: "pool_address"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"protocol_version", "token0_address", "token1_address", "fee_tier",
			"resolution_status", "rejection_reason", "quote_eligible",
			"tvl_usd", "volume_24h_usd",
			"block_number", "block_timestamp", "last_seen_at", "raw_metadata",
		}),
	}).CreateInBatches(items, 250).Error
}

func (m *marketAggregationDB) UpsertDexRoutes(items []DexRouteCurrent) error {
	if len(items) == 0 {
		return nil
	}
	for index := range items {
		if len(items[index].Path) == 0 {
			items[index].Path = datatypes.JSON([]byte(`[]`))
		}
		if len(items[index].PoolAddresses) == 0 {
			items[index].PoolAddresses = datatypes.JSON([]byte(`[]`))
		}
		if len(items[index].ProtocolVersions) == 0 {
			items[index].ProtocolVersions = datatypes.JSON([]byte(`[]`))
		}
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider"}, {Name: "asset_guid"}, {Name: "route_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"chain_id", "price_usd", "buy_price_usd", "sell_price_usd",
			"change_24h_pct", "turnover_24h_usd", "tvl_usd", "price_impact_pct",
			"round_trip_spread_pct", "quote_notional_usd", "block_number",
			"quote_reference_kind", "block_timestamp", "quality", "available", "path", "pool_addresses",
			"protocol_versions", "unavailable_reason", "observed_at", "updated_at",
		}),
	}).CreateInBatches(items, 100).Error
}

func (m *marketAggregationDB) MarkUnselectedDexRoutesUnavailable(
	provider string,
	routeKeys []string,
	now time.Time,
) error {
	provider = normalizedProvider(provider)
	if provider == "" || len(routeKeys) == 0 {
		return nil
	}
	return m.gorm.Model(&DexRouteCurrent{}).
		Where("provider = ? AND route_key NOT IN ?", provider, routeKeys).
		Updates(map[string]interface{}{
			"available":          false,
			"unavailable_reason": "route_not_selected",
			"updated_at":         now.UTC(),
		}).Error
}

func (m *marketAggregationDB) QueryDexRoutes(assetID string) ([]DexRouteCurrent, error) {
	var rows []DexRouteCurrent
	err := m.gorm.Where("asset_guid = ?", strings.TrimSpace(assetID)).
		Order("provider ASC, available DESC, quality DESC, route_key ASC").
		Find(&rows).Error
	return rows, err
}

func (m *marketAggregationDB) QueryPublishedDexRoutes(assetID string) ([]DexRouteCurrent, error) {
	rows, err := m.QueryDexRoutes(assetID)
	if err != nil {
		return nil, err
	}
	allowedByProvider := make(map[string]map[string]struct{})
	result := make([]DexRouteCurrent, 0, len(rows))
	for _, row := range rows {
		if row.UnavailableReason != nil &&
			strings.EqualFold(strings.TrimSpace(*row.UnavailableReason), "route_not_selected") {
			continue
		}
		allowed, loaded := allowedByProvider[row.Provider]
		if !loaded {
			var queryErr error
			allowed, _, queryErr = m.QueryPublishedAssetIDs(row.Provider)
			if queryErr != nil {
				return nil, queryErr
			}
			allowedByProvider[row.Provider] = allowed
		}
		if _, ok := allowed[row.AssetGuid]; ok {
			result = append(result, row)
		}
	}
	return result, nil
}

func (m *marketAggregationDB) InsertDexQuoteObservations(items []DexQuoteObservation) error {
	if len(items) == 0 {
		return nil
	}
	return m.gorm.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(items, 100).Error
}

func (m *marketAggregationDB) QueryDexWindowCoverage(
	provider, assetID, routeKey, quoteNotionalUSD string,
	start, end time.Time,
) (*DexWindowCoverage, error) {
	var row DexWindowCoverage
	result := m.gorm.Raw(`
		WITH observations AS (
			SELECT observed_at,
			       price_usd,
			       EXTRACT(EPOCH FROM (
			observed_at - LAG(observed_at) OVER (ORDER BY observed_at)
			       )) AS gap_seconds
			FROM dex_quote_observation
			WHERE provider = ?
			  AND asset_guid = ?
			  AND route_key = ?
			  AND quote_notional_usd = ?::numeric
			  AND observed_at BETWEEN ? AND ?
		),
		summary AS (
			SELECT MIN(observed_at) AS first_observed_at,
			       MAX(observed_at) AS last_observed_at,
			       COUNT(*) AS observation_count,
			       COALESCE(MAX(gap_seconds), 0) AS max_gap_seconds
			FROM observations
		),
		open_observation AS (
			SELECT price_usd AS open_price_usd
			FROM observations
			ORDER BY observed_at ASC
			LIMIT 1
		)
		SELECT summary.*, open_observation.open_price_usd
		FROM summary
		CROSS JOIN open_observation`,
		normalizedProvider(provider), strings.TrimSpace(assetID),
		strings.TrimSpace(routeKey), strings.TrimSpace(quoteNotionalUSD),
		start.UTC(), end.UTC(),
	).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 || row.ObservationCount == 0 {
		return nil, nil
	}
	row.MaxGap = time.Duration(row.MaxGapSeconds * float64(time.Second))
	return &row, nil
}

func (m *marketAggregationDB) PruneDexQuoteObservations(before time.Time) error {
	return m.gorm.Where("observed_at < ?", before).Delete(&DexQuoteObservation{}).Error
}
