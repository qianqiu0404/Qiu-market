package database

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidDashboardVenue = errors.New("invalid market venue")

type AssetAlias struct {
	Provider     string     `gorm:"column:provider;primaryKey"`
	Alias        string     `gorm:"column:alias;primaryKey"`
	AssetGuid    string     `gorm:"column:asset_guid"`
	ReviewStatus string     `gorm:"column:review_status"`
	ReviewedBy   *string    `gorm:"column:reviewed_by"`
	ReviewedAt   *time.Time `gorm:"column:reviewed_at"`
	ReviewSource *string    `gorm:"column:review_source"`
	ReviewNote   *string    `gorm:"column:review_note"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

func (AssetAlias) TableName() string { return "asset_alias" }

type ProviderMarketCandidate struct {
	Provider         string         `gorm:"column:provider;primaryKey"`
	SourceSymbol     string         `gorm:"column:source_symbol;primaryKey"`
	MarketType       string         `gorm:"column:market_type;primaryKey"`
	BaseAlias        string         `gorm:"column:base_alias"`
	QuoteAlias       string         `gorm:"column:quote_alias"`
	UpstreamStatus   *string        `gorm:"column:upstream_status"`
	ResolutionStatus string         `gorm:"column:resolution_status"`
	BaseAssetGuid    *string        `gorm:"column:base_asset_guid"`
	QuoteAssetGuid   *string        `gorm:"column:quote_asset_guid"`
	RejectionReason  *string        `gorm:"column:rejection_reason"`
	FirstSeenAt      time.Time      `gorm:"column:first_seen_at"`
	LastSeenAt       time.Time      `gorm:"column:last_seen_at"`
	ResolvedAt       *time.Time     `gorm:"column:resolved_at"`
	EnabledAt        *time.Time     `gorm:"column:enabled_at"`
	RawMetadata      datatypes.JSON `gorm:"column:raw_metadata"`
}

func (ProviderMarketCandidate) TableName() string { return "provider_market_candidate" }

type AssetMetricCurrent struct {
	AssetGuid         string     `gorm:"column:asset_guid;primaryKey"`
	Provider          string     `gorm:"column:provider"`
	ProviderAssetID   string     `gorm:"column:provider_asset_id"`
	MarketCapRank     *int       `gorm:"column:market_cap_rank"`
	ReferencePriceUSD *string    `gorm:"column:reference_price_usd"`
	MarketCapUSD      *string    `gorm:"column:market_cap_usd"`
	CirculatingSupply *string    `gorm:"column:circulating_supply"`
	TotalSupply       *string    `gorm:"column:total_supply"`
	MaxSupply         *string    `gorm:"column:max_supply"`
	ImageURL          *string    `gorm:"column:image_url"`
	ProviderUpdatedAt *time.Time `gorm:"column:provider_updated_at"`
	ObservedAt        time.Time  `gorm:"column:observed_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

func (AssetMetricCurrent) TableName() string { return "asset_metric_current" }

type MarketGlobalMetric struct {
	Provider          string     `gorm:"column:provider;primaryKey"`
	TotalMarketCapUSD *string    `gorm:"column:total_market_cap_usd"`
	TotalVolume24hUSD *string    `gorm:"column:total_volume_24h_usd"`
	BTCDominancePct   *string    `gorm:"column:btc_dominance_pct"`
	ProviderUpdatedAt *time.Time `gorm:"column:provider_updated_at"`
	ObservedAt        time.Time  `gorm:"column:observed_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

func (MarketGlobalMetric) TableName() string { return "market_global_metric" }

type AssetPriceIndex struct {
	AssetGuid        string         `gorm:"column:asset_guid;primaryKey"`
	PriceUSD         *string        `gorm:"column:price_usd"`
	Open24hUSD       *string        `gorm:"column:open_24h_usd"`
	Change24hPct     *string        `gorm:"column:change_24h_pct"`
	Turnover24hUSD   *string        `gorm:"column:turnover_24h_usd"`
	ContributorCount int            `gorm:"column:contributor_count"`
	Confidence       string         `gorm:"column:confidence"`
	Available        bool           `gorm:"column:available"`
	Version          int64          `gorm:"column:version;default:(nextval('asset_price_index_version_seq'))"`
	ObservedAt       time.Time      `gorm:"column:observed_at"`
	Contributors     datatypes.JSON `gorm:"column:contributors"`
	Exclusions       datatypes.JSON `gorm:"column:exclusions"`
	CreatedAt        time.Time      `gorm:"column:created_at"`
	UpdatedAt        time.Time      `gorm:"column:updated_at"`
}

func (AssetPriceIndex) TableName() string { return "asset_price_index" }

type CompositeMarketCandidate struct {
	AssetID          string     `gorm:"column:asset_id"`
	MarketID         string     `gorm:"column:market_id"`
	MarketCode       string     `gorm:"column:market_code"`
	Provider         string     `gorm:"column:provider"`
	MarketType       string     `gorm:"column:market_type"`
	QuoteAsset       string     `gorm:"column:quote_asset"`
	Price            string     `gorm:"column:price"`
	Open24h          *string    `gorm:"column:open_24h"`
	Change24hPct     *string    `gorm:"column:change_24h_pct"`
	QuoteTurnover24h *string    `gorm:"column:quote_turnover_24h"`
	ObservedAt       *time.Time `gorm:"column:observed_at"`
	SourceTime       *time.Time `gorm:"column:source_time"`
}

type AssetIndexDashboardQuery struct {
	Page             int64
	PageSize         int64
	Venue            string
	Universe         string
	IncludeUncovered bool
	Search           string
	Filter           string
	SortBy           string
	SortDirection    string
}

type AssetIndexDashboardRow struct {
	AssetID               string         `gorm:"column:asset_id"`
	AssetSymbol           string         `gorm:"column:asset_symbol"`
	AssetName             string         `gorm:"column:asset_name"`
	Logo                  string         `gorm:"column:logo"`
	Rank                  *int           `gorm:"column:rank"`
	SelectionVersion      int64          `gorm:"column:selection_version"`
	SelectionRank         int            `gorm:"column:selection_rank"`
	Price                 *string        `gorm:"column:price"`
	CompositePrice        *string        `gorm:"column:composite_price"`
	MarketReferencePrice  *string        `gorm:"column:market_reference_price"`
	DisplayPrice          *string        `gorm:"column:display_price"`
	DisplayPriceKind      string         `gorm:"column:display_price_kind"`
	DisplayChange24hPct   *string        `gorm:"column:display_change_24h_pct"`
	DisplayChangeKind     string         `gorm:"column:display_change_kind"`
	DisplayAvailable      bool           `gorm:"column:display_available"`
	DisplayObservedAt     *time.Time     `gorm:"column:display_observed_at"`
	DexRouteAvailable     bool           `gorm:"column:dex_route_available"`
	Change24hPct          *string        `gorm:"column:change_24h_pct"`
	VenuePriceVersion     int64          `gorm:"column:venue_price_version"`
	CompositeChange24h    *string        `gorm:"column:composite_change_24h_pct"`
	CompositeTurnover24h  *string        `gorm:"column:composite_turnover_24h_usd"`
	CompositeCount        int            `gorm:"column:composite_contributor_count"`
	CompositeConfidence   string         `gorm:"column:composite_confidence"`
	CompositeContributors datatypes.JSON `gorm:"column:composite_contributors"`
	CompositeObservedAt   *time.Time     `gorm:"column:composite_observed_at"`
	CompositeVersion      int64          `gorm:"column:composite_version"`
	ReferenceObservedAt   *time.Time     `gorm:"column:reference_observed_at"`
	ReferenceSourceTime   *time.Time     `gorm:"column:reference_source_time"`
	MarketCapUSD          *string        `gorm:"column:market_cap_usd"`
	Turnover24hUSD        *string        `gorm:"column:turnover_24h_usd"`
	CirculatingSupply     *string        `gorm:"column:circulating_supply"`
	SpotMarketCount       int64          `gorm:"column:spot_market_count"`
	PerpMarketCount       int64          `gorm:"column:perp_market_count"`
	DexRouteCount         int64          `gorm:"column:dex_route_count"`
	ContributorCount      int            `gorm:"column:contributor_count"`
	PricedVenueCount      int            `gorm:"column:priced_venue_count"`
	Confidence            string         `gorm:"column:confidence"`
	Quality               string         `gorm:"column:quality"`
	PriceKind             string         `gorm:"column:price_kind"`
	PriceSource           string         `gorm:"column:price_source"`
	CoverageStatus        string         `gorm:"column:coverage_status"`
	CoverageReason        string         `gorm:"column:coverage_reason"`
	FreshnessStatus       string         `gorm:"column:freshness_status"`
	FreshnessAgeSeconds   *int64         `gorm:"column:freshness_age_seconds"`
	Available             bool           `gorm:"column:available"`
	SourceTime            *time.Time     `gorm:"column:source_time"`
	ObservedAt            *time.Time     `gorm:"column:observed_at"`
	ProviderUpdatedAt     *time.Time     `gorm:"column:provider_updated_at"`
	LatestAvailable       bool           `gorm:"column:latest_available"`
	LatestObservedAt      *time.Time     `gorm:"column:latest_observed_at"`
	LastAttemptAt         *time.Time     `gorm:"column:last_attempt_at"`
	LastSuccessAt         *time.Time     `gorm:"column:last_success_at"`
	LastErrorClass        *string        `gorm:"column:last_error_class"`
}

type MarketPriceTickQuery struct {
	Venue    string
	AssetIDs []string
}

type MarketPriceTickRow struct {
	AssetID          string         `gorm:"column:asset_id"`
	Provider         string         `gorm:"column:provider"`
	PriceKind        string         `gorm:"column:price_kind"`
	PriceUSD         *string        `gorm:"column:price_usd"`
	Change24hPct     *string        `gorm:"column:change_24h_pct"`
	Turnover24hUSD   *string        `gorm:"column:turnover_24h_usd"`
	ContributorCount int            `gorm:"column:contributor_count"`
	Confidence       string         `gorm:"column:confidence"`
	Quality          string         `gorm:"column:quality"`
	Contributors     datatypes.JSON `gorm:"column:contributors"`
	Available        bool           `gorm:"column:available"`
	SourceTime       *time.Time     `gorm:"column:source_time"`
	ObservedAt       *time.Time     `gorm:"column:observed_at"`
	LastSuccessAt    *time.Time     `gorm:"column:last_success_at"`
	Version          int64          `gorm:"column:version"`
}

type AssetMarketReadRow struct {
	MarketID             string     `gorm:"column:market_id"`
	MarketCode           string     `gorm:"column:market_code"`
	Provider             string     `gorm:"column:provider"`
	Symbol               string     `gorm:"column:symbol"`
	MarketType           string     `gorm:"column:market_type"`
	QuoteAsset           string     `gorm:"column:quote_asset"`
	Price                string     `gorm:"column:price"`
	Change24hPct         *string    `gorm:"column:change_24h_pct"`
	Turnover24h          *string    `gorm:"column:turnover_24h"`
	ObservedAt           *time.Time `gorm:"column:observed_at"`
	SourceTime           *time.Time `gorm:"column:source_time"`
	SourceTimeKind       *string    `gorm:"column:source_time_kind"`
	HasKline             bool       `gorm:"column:has_kline"`
	CompositeContributor bool       `gorm:"column:composite_contributor"`
}

type CatalogAuditRow struct {
	Provider         string    `gorm:"column:provider"`
	SourceSymbol     string    `gorm:"column:source_symbol"`
	MarketType       string    `gorm:"column:market_type"`
	BaseAlias        string    `gorm:"column:base_alias"`
	QuoteAlias       string    `gorm:"column:quote_alias"`
	UpstreamStatus   *string   `gorm:"column:upstream_status"`
	ResolutionStatus string    `gorm:"column:resolution_status"`
	BaseAssetGuid    *string   `gorm:"column:base_asset_guid"`
	QuoteAssetGuid   *string   `gorm:"column:quote_asset_guid"`
	RejectionReason  *string   `gorm:"column:rejection_reason"`
	LastSeenAt       time.Time `gorm:"column:last_seen_at"`
	Rank             *int      `gorm:"column:rank"`
	CandidateKind    string    `gorm:"column:candidate_kind"`
	AliasReview      string    `gorm:"column:alias_review"`
	RolloutMode      string    `gorm:"column:rollout_mode"`
	ResolutionSource string    `gorm:"column:resolution_source"`
}

type CatalogAuditCount struct {
	Status string `gorm:"column:status"`
	Count  int64  `gorm:"column:count"`
}

type AssetIndexSummary struct {
	AssetCount                  int64      `gorm:"column:asset_count"`
	Top50UniverseCount          int64      `gorm:"column:top50_universe_count"`
	EligibleAssetCount          int64      `gorm:"column:eligible_asset_count"`
	PublishedAssetCount         int64      `gorm:"column:published_asset_count"`
	PricedAssetCount            int64      `gorm:"column:priced_asset_count"`
	DisplayedAssetCount         int64      `gorm:"column:displayed_asset_count"`
	RoutableAssetCount          int64      `gorm:"column:routable_asset_count"`
	ReferenceOnlyAssetCount     int64      `gorm:"column:reference_only_asset_count"`
	UnpricedAssetCount          int64      `gorm:"column:unpriced_asset_count"`
	ChangeAvailableCount        int64      `gorm:"column:change_available_count"`
	Advancers                   int64      `gorm:"column:advancers"`
	Decliners                   int64      `gorm:"column:decliners"`
	Flat                        int64      `gorm:"column:flat"`
	Unknown                     int64      `gorm:"column:unknown"`
	CoveredVolume               *string    `gorm:"column:covered_volume"`
	ObservedAt                  *time.Time `gorm:"column:observed_at"`
	ContributingProviderCount   int64      `gorm:"column:contributing_provider_count"`
	SingleVenuePricedAssetCount int64      `gorm:"column:single_venue_priced_asset_count"`
	MultiVenuePricedAssetCount  int64      `gorm:"column:multi_venue_priced_asset_count"`
	LocalPreviewEnabled         bool       `gorm:"-"`
	PreviewSourceKey            string     `gorm:"-"`
	PreviewCoveredCount         int64      `gorm:"-"`
}

type ProviderFeedMarket struct {
	SourceSymbol string `gorm:"column:source_symbol"`
	AssetID      string `gorm:"column:asset_id"`
	Rank         int    `gorm:"column:rank"`
}

type VenueAssetEvidence struct {
	AssetID   string `gorm:"column:asset_id"`
	HasChange bool   `gorm:"column:has_change"`
}

type MarketAggregationDB interface {
	UpsertProviderMarketCandidates([]ProviderMarketCandidate) error
	UpsertAssetExternalMappings([]AssetExternalMapping) error
	UpsertAssetAliases([]AssetAlias) error
	QueryApprovedAliases(provider string) (map[string]string, error)
	UpsertAssetMetrics([]AssetMetricCurrent) error
	UpsertGlobalMetric(MarketGlobalMetric) error
	QueryTopAssetIDs(limit int) (map[string]struct{}, error)
	QueryUniqueTopAssetSymbols(limit int) (map[string]string, error)
	QueryUSDReferenceRates(maxAge time.Duration, now time.Time) (map[string]string, error)
	QueryCompositeMarketCandidates() ([]CompositeMarketCandidate, error)
	UpsertAssetPriceIndexes([]AssetPriceIndex) error
	QueryAssetIndexDashboard(AssetIndexDashboardQuery) ([]AssetIndexDashboardRow, int64, error)
	QueryMarketPriceTicks(MarketPriceTickQuery) ([]MarketPriceTickRow, error)
	QueryAssetMarkets(assetID string) ([]AssetMarketReadRow, error)
	QueryCatalogAudit(provider, status string, rankLimit int, page, pageSize int64) ([]CatalogAuditRow, []CatalogAuditCount, int64, error)
	QueryGlobalMetric(provider string) (*MarketGlobalMetric, error)
	QueryAssetPriceIndex(assetID string) (*AssetPriceIndex, error)
	QueryAssetIndexSummary(venue string) (*AssetIndexSummary, error)
	EnableResolvedSpotMarkets(provider string, catalogObservedAt time.Time, allowedAssetIDs map[string]struct{}) (int64, error)
	ReconcileResolvedSpotMarkets(provider string) (int64, error)
	QueryProviderRolloutStates() ([]ProviderRolloutState, error)
	QueryProviderRollout(provider string) (*ProviderRolloutState, error)
	QueryProviderAssetSelectionState(provider string) (*ProviderAssetSelectionState, error)
	QueryProviderAssetSelection(provider string) ([]ProviderAssetSelection, *ProviderAssetSelectionState, error)
	RefreshProviderAssetSelection(provider string, targetCount int, reason string) (*ProviderAssetSelectionState, error)
	EnsureProviderAssetSelection(provider string, targetCount int, reason string) (*ProviderAssetSelectionState, error)
	QueryProviderSelectedAssetIDs(provider string) (map[string]struct{}, *ProviderAssetSelectionState, error)
	QuerySelectedAssetUnionIDs() (map[string]struct{}, error)
	SetProviderRollout(provider, mode string, rankLimit int, canaryAssetIDs []string, minSoakUntil *time.Time) error
	SetProviderLocalPreview(provider string, enabled bool) error
	QueryEligibleProviderAssetIDs(provider string, rankLimit int) ([]string, error)
	QueryEligibleProviderFeedMarkets(provider string, rankLimit int) ([]ProviderFeedMarket, error)
	QueryRolloutAssetIDs(provider string) (map[string]struct{}, *ProviderRolloutState, error)
	QueryPublishedAssetIDs(provider string) (map[string]struct{}, *ProviderRolloutState, error)
	QueryFreshVenueAssetIDs(provider, priceKind string, since time.Time) ([]string, error)
	QueryFreshVenueAssetEvidence(provider, priceKind string, since time.Time) ([]VenueAssetEvidence, error)
	ApplyReviewedAssetAliases([]AssetAlias) error
	UpsertAssetRepresentations([]AssetRepresentation) error
	QueryApprovedAssetRepresentations(chainID int64) ([]AssetRepresentation, error)
	UpsertAssetVenueSnapshots([]AssetVenueSnapshot) error
	ReplaceDexVenueSnapshots([]AssetVenueSnapshot) error
	UpsertDexPoolCandidates([]DexPoolCandidate) error
	UpsertDexRoutes([]DexRouteCurrent) error
	MarkUnselectedDexRoutesUnavailable(provider string, routeKeys []string, now time.Time) error
	QueryDexRoutes(assetID string) ([]DexRouteCurrent, error)
	QueryPublishedDexRoutes(assetID string) ([]DexRouteCurrent, error)
	InsertDexQuoteObservations([]DexQuoteObservation) error
	QueryDexWindowCoverage(provider, assetID, routeKey, quoteNotionalUSD string, start, end time.Time) (*DexWindowCoverage, error)
	PruneDexQuoteObservations(before time.Time) error
}

func (m *marketAggregationDB) QueryEligibleProviderFeedMarkets(
	provider string,
	rankLimit int,
) ([]ProviderFeedMarket, error) {
	provider = normalizedProvider(provider)
	if rankLimit < 1 {
		rankLimit = 50
	}
	var rows []ProviderFeedMarket
	err := m.gorm.Table("provider_market_candidate candidate").
		Select(`candidate.source_symbol,
			candidate.base_asset_guid AS asset_id,
			metric.market_cap_rank AS rank`).
		Joins("JOIN asset_metric_current metric ON metric.asset_guid = candidate.base_asset_guid").
		Where(`candidate.provider = ?
			AND candidate.market_type = 'spot'
			AND candidate.resolution_status IN ('resolved', 'enabled')
			AND candidate.base_asset_guid IS NOT NULL
			AND metric.market_cap_rank BETWEEN 1 AND ?`,
			provider, rankLimit).
		Group("candidate.source_symbol, candidate.base_asset_guid, metric.market_cap_rank").
		Order("metric.market_cap_rank ASC, candidate.base_asset_guid ASC, candidate.source_symbol ASC").
		Scan(&rows).Error
	return rows, err
}

type marketAggregationDB struct{ gorm *gorm.DB }

func NewMarketAggregationDB(db *gorm.DB) MarketAggregationDB { return &marketAggregationDB{gorm: db} }

func (m *marketAggregationDB) UpsertProviderMarketCandidates(items []ProviderMarketCandidate) error {
	if len(items) == 0 {
		return nil
	}
	for index := range items {
		if len(items[index].RawMetadata) == 0 {
			items[index].RawMetadata = datatypes.JSON([]byte(`{}`))
		}
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider"}, {Name: "source_symbol"}, {Name: "market_type"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"base_alias", "quote_alias", "upstream_status", "resolution_status",
			"base_asset_guid", "quote_asset_guid", "rejection_reason", "last_seen_at",
			"resolved_at", "enabled_at", "raw_metadata",
		}),
	}).CreateInBatches(items, 500).Error
}

func (m *marketAggregationDB) UpsertAssetExternalMappings(items []AssetExternalMapping) error {
	if len(items) == 0 {
		return nil
	}
	return m.gorm.Table("asset_external_mapping").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "asset_guid"}},
		DoUpdates: clause.AssignmentColumns([]string{"external_id", "updated_at"}),
	}).CreateInBatches(items, 250).Error
}

func (m *marketAggregationDB) UpsertAssetAliases(items []AssetAlias) error {
	if len(items) == 0 {
		return nil
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "alias"}},
		DoNothing: true,
	}).CreateInBatches(items, 250).Error
}

func (m *marketAggregationDB) QueryApprovedAliases(provider string) (map[string]string, error) {
	var aliases []AssetAlias
	if err := m.gorm.Where("provider = ? AND review_status = 'approved'", normalizedProvider(provider)).
		Find(&aliases).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		result[strings.ToUpper(alias.Alias)] = alias.AssetGuid
	}
	return result, nil
}

func (m *marketAggregationDB) UpsertAssetMetrics(items []AssetMetricCurrent) error {
	if len(items) == 0 {
		return nil
	}
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "asset_guid"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider", "provider_asset_id", "market_cap_rank", "reference_price_usd",
			"market_cap_usd", "circulating_supply", "total_supply", "max_supply",
			"image_url", "provider_updated_at", "observed_at", "updated_at",
		}),
	}).CreateInBatches(items, 250).Error
}

func (m *marketAggregationDB) UpsertGlobalMetric(metric MarketGlobalMetric) error {
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"total_market_cap_usd", "total_volume_24h_usd", "btc_dominance_pct",
			"provider_updated_at", "observed_at", "updated_at",
		}),
	}).Create(&metric).Error
}

func (m *marketAggregationDB) QueryTopAssetIDs(limit int) (map[string]struct{}, error) {
	if limit <= 0 {
		limit = 200
	}
	var ids []string
	if err := m.gorm.Table("asset_metric_current").
		Where("market_cap_rank BETWEEN 1 AND ?", limit).
		Order("market_cap_rank ASC").
		Pluck("asset_guid", &ids).Error; err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		result[id] = struct{}{}
	}
	return result, nil
}

func (m *marketAggregationDB) QueryUniqueTopAssetSymbols(limit int) (map[string]string, error) {
	if limit <= 0 {
		limit = 200
	}
	type row struct {
		Symbol    string `gorm:"column:symbol"`
		AssetGuid string `gorm:"column:asset_guid"`
	}
	var rows []row
	if err := m.gorm.Table("asset_metric_current am").
		Select("upper(a.asset_symbol) AS symbol, min(a.guid) AS asset_guid").
		Joins("JOIN asset a ON a.guid = am.asset_guid").
		Where("am.market_cap_rank BETWEEN 1 AND ?", limit).
		Group("upper(a.asset_symbol)").
		Having("COUNT(*) = 1").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, row := range rows {
		result[row.Symbol] = row.AssetGuid
	}
	return result, nil
}

func (m *marketAggregationDB) QueryUSDReferenceRates(maxAge time.Duration, now time.Time) (map[string]string, error) {
	type row struct {
		Symbol string `gorm:"column:symbol"`
		Rate   string `gorm:"column:rate"`
	}
	var rows []row
	if err := m.gorm.Table("asset_metric_current am").
		Select("upper(a.asset_symbol) AS symbol, am.reference_price_usd AS rate").
		Joins("JOIN asset a ON a.guid = am.asset_guid").
		Where("upper(a.asset_symbol) IN ? AND am.reference_price_usd > 0 AND am.observed_at >= ?",
			[]string{"USDT", "USDC"}, now.Add(-maxAge)).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := map[string]string{"USD": "1"}
	for _, row := range rows {
		result[row.Symbol] = row.Rate
	}
	return result, nil
}

func (m *marketAggregationDB) QueryCompositeMarketCandidates() ([]CompositeMarketCandidate, error) {
	var rows []CompositeMarketCandidate
	err := m.gorm.Table("symbol_market sm").
		Select(`s.base_asset_guid AS asset_id,
			es.guid AS market_id,
			es.market_code,
			e.code AS provider,
			lower(s.market_type) AS market_type,
			upper(qa.asset_symbol) AS quote_asset,
			sm.price,
			sm.open_24h,
			sm.change_24h_pct,
			COALESCE(sm.quote_turnover_24h, sm.volume) AS quote_turnover_24h,
			sm.observed_at,
			sm.source_time`).
		Joins("JOIN exchange_symbol es ON es.guid = sm.market_id").
		Joins("JOIN exchange e ON e.guid = es.exchange_guid").
		Joins("JOIN provider_rollout_state rollout ON rollout.provider = e.code").
		Joins("JOIN symbol s ON s.guid = es.symbol_guid").
		Joins("JOIN asset qa ON qa.guid = s.qoute_asset_guid").
		Joins(`JOIN provider_asset_selection_state selection_state
			ON selection_state.provider = e.code`).
		Joins(`JOIN provider_asset_selection selection
			ON selection.provider = selection_state.provider
			AND selection.selection_version = selection_state.active_version
			AND selection.asset_guid = s.base_asset_guid`).
		Where(`sm.is_active = TRUE
			AND es.is_active = TRUE
			AND s.is_active = TRUE
			AND (
				rollout.local_preview_enabled = TRUE
				OR rollout.mode = 'enabled'
				OR (
					rollout.mode = 'canary'
					AND rollout.canary_asset_ids @> jsonb_build_array(s.base_asset_guid)
				)
			)`).
		Order("s.base_asset_guid ASC, e.code ASC, es.guid ASC").
		Scan(&rows).Error
	return rows, err
}

func (m *marketAggregationDB) UpsertAssetPriceIndexes(items []AssetPriceIndex) error {
	if len(items) == 0 {
		return nil
	}
	for index := range items {
		if len(items[index].Contributors) == 0 {
			items[index].Contributors = datatypes.JSON([]byte(`[]`))
		}
		if len(items[index].Exclusions) == 0 {
			items[index].Exclusions = datatypes.JSON([]byte(`[]`))
		}
	}
	materialChange := `ROW(
		asset_price_index.price_usd,
		asset_price_index.open_24h_usd,
		asset_price_index.change_24h_pct,
		asset_price_index.turnover_24h_usd,
		asset_price_index.contributor_count,
		asset_price_index.confidence,
		asset_price_index.available,
		asset_price_index.contributors,
		asset_price_index.exclusions
	) IS DISTINCT FROM ROW(
		EXCLUDED.price_usd,
		EXCLUDED.open_24h_usd,
		EXCLUDED.change_24h_pct,
		EXCLUDED.turnover_24h_usd,
		EXCLUDED.contributor_count,
		EXCLUDED.confidence,
		EXCLUDED.available,
		EXCLUDED.contributors,
		EXCLUDED.exclusions
	)`
	return m.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "asset_guid"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"price_usd":         gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.price_usd ELSE asset_price_index.price_usd END"),
			"open_24h_usd":      gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.open_24h_usd ELSE asset_price_index.open_24h_usd END"),
			"change_24h_pct":    gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.change_24h_pct ELSE asset_price_index.change_24h_pct END"),
			"turnover_24h_usd":  gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.turnover_24h_usd ELSE asset_price_index.turnover_24h_usd END"),
			"contributor_count": gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.contributor_count ELSE asset_price_index.contributor_count END"),
			"confidence":        gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.confidence ELSE asset_price_index.confidence END"),
			"available":         gorm.Expr("asset_price_index.available OR EXCLUDED.available"),
			"version":           gorm.Expr("CASE WHEN EXCLUDED.available AND (" + materialChange + ") THEN nextval('asset_price_index_version_seq') ELSE asset_price_index.version END"),
			"observed_at":       gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.observed_at ELSE asset_price_index.observed_at END"),
			"contributors":      gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.contributors ELSE asset_price_index.contributors END"),
			"exclusions":        gorm.Expr("CASE WHEN EXCLUDED.available THEN EXCLUDED.exclusions ELSE asset_price_index.exclusions END"),
			"updated_at":        gorm.Expr("CASE WHEN EXCLUDED.available AND (" + materialChange + ") THEN clock_timestamp() ELSE asset_price_index.updated_at END"),
		}),
	}).CreateInBatches(items, 250).Error
}

type dashboardDisplayExpressions struct {
	price      string
	priceKind  string
	change     string
	changeKind string
	available  string
	observedAt string
	dexRoute   string
}

func dashboardDisplay(venue string) dashboardDisplayExpressions {
	if venue == "uniswap" || venue == "pancakeswap" {
		const routeFresh = "venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '60 seconds'"
		return dashboardDisplayExpressions{
			price: `CASE
				WHEN ` + routeFresh + ` AND venue_snapshot.price_usd IS NOT NULL THEN venue_snapshot.price_usd
				WHEN composite.price_usd IS NOT NULL THEN composite.price_usd
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN am.reference_price_usd
			END`,
			priceKind: `CASE
				WHEN ` + routeFresh + ` AND venue_snapshot.price_usd IS NOT NULL THEN 'dex_route'
				WHEN composite.price_usd IS NOT NULL THEN 'composite_reference'
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN 'market_reference'
				ELSE 'unavailable'
			END`,
			change: `CASE
				WHEN ` + routeFresh + ` AND venue_snapshot.change_24h_pct IS NOT NULL
				THEN venue_snapshot.change_24h_pct
				WHEN composite.change_24h_pct IS NOT NULL THEN composite.change_24h_pct
			END`,
			changeKind: `CASE
				WHEN ` + routeFresh + ` AND venue_snapshot.change_24h_pct IS NOT NULL
				THEN 'dex_route'
				WHEN composite.change_24h_pct IS NOT NULL THEN 'composite_reference'
				ELSE 'unavailable'
			END`,
			available: `(` + routeFresh + ` AND venue_snapshot.price_usd IS NOT NULL)
				OR composite.price_usd IS NOT NULL
				OR (am.reference_price_usd IS NOT NULL
					AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes')`,
			observedAt: `CASE
				WHEN ` + routeFresh + ` AND venue_snapshot.price_usd IS NOT NULL
				THEN venue_snapshot.last_success_at
				WHEN composite.price_usd IS NOT NULL THEN composite.observed_at
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN am.observed_at
			END`,
			dexRoute: `(` + routeFresh + ` AND venue_snapshot.price_usd IS NOT NULL)`,
		}
	}
	const venueVisible = "venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes'"
	return dashboardDisplayExpressions{
		price:      `CASE WHEN ` + venueVisible + ` THEN venue_snapshot.price_usd END`,
		priceKind:  `CASE WHEN ` + venueVisible + ` AND venue_snapshot.price_usd IS NOT NULL THEN COALESCE(venue_snapshot.price_kind, 'unavailable') ELSE 'unavailable' END`,
		change:     `CASE WHEN ` + venueVisible + ` THEN venue_snapshot.change_24h_pct END`,
		changeKind: `CASE WHEN ` + venueVisible + ` AND venue_snapshot.change_24h_pct IS NOT NULL THEN COALESCE(venue_snapshot.price_kind, 'unavailable') ELSE 'unavailable' END`,
		available:  `(` + venueVisible + ` AND venue_snapshot.price_usd IS NOT NULL)`,
		observedAt: `CASE WHEN ` + venueVisible + ` AND venue_snapshot.price_usd IS NOT NULL THEN venue_snapshot.last_success_at END`,
		dexRoute:   "FALSE",
	}
}

func assetDashboardOrder(sortBy, direction string, display dashboardDisplayExpressions) string {
	dir := "DESC"
	if strings.EqualFold(direction, "asc") {
		dir = "ASC"
	}
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "asset", "symbol":
		return "upper(a.asset_symbol) " + dir + ", am.asset_guid ASC"
	case "price":
		return display.price + " " + dir + " NULLS LAST, am.asset_guid ASC"
	case "change24h":
		return display.change + " " + dir + " NULLS LAST, am.asset_guid ASC"
	case "volume", "turnover24h":
		return "CASE WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '30 seconds' THEN venue_snapshot.turnover_24h_usd END " + dir + " NULLS LAST, am.asset_guid ASC"
	case "market_cap":
		return "am.market_cap_usd " + dir + " NULLS LAST, am.market_cap_rank ASC NULLS LAST, am.asset_guid ASC"
	default:
		return "am.market_cap_rank ASC NULLS LAST, venue_snapshot.turnover_24h_usd DESC NULLS LAST, am.asset_guid ASC"
	}
}

func (m *marketAggregationDB) queryPublishedProviderAssetIDs(provider string) (map[string]struct{}, error) {
	provider = normalizedProvider(provider)
	allowed, rollout, err := m.QueryPublishedAssetIDs(provider)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{})
	if rollout == nil || len(allowed) == 0 {
		return result, nil
	}
	var ids []string
	if provider == "uniswap" || provider == "pancakeswap" {
		chainID := int64(1)
		if provider == "pancakeswap" {
			chainID = 56
		}
		if err := m.gorm.Table("asset_representation representation").
			Select("representation.asset_guid").
			Joins(`JOIN dex_pool_candidate pool
				ON pool.provider = ?
				AND pool.chain_id = representation.chain_id
				AND pool.resolution_status IN ('resolved', 'enabled')
				AND (
					pool.token0_address = representation.contract_address
					OR pool.token1_address = representation.contract_address
				)`, provider).
			Where(`representation.chain_id = ?
				AND representation.review_status = 'approved'`, chainID).
			Group("representation.asset_guid").
			Pluck("representation.asset_guid", &ids).Error; err != nil {
			return nil, err
		}
	} else {
		marketType := "spot"
		if provider == "hyperliquid" {
			marketType = "perp"
		}
		if err := m.gorm.Table("exchange_symbol market").
			Select("symbol_row.base_asset_guid").
			Joins("JOIN exchange venue ON venue.guid = market.exchange_guid").
			Joins("JOIN symbol symbol_row ON symbol_row.guid = market.symbol_guid").
			Where(`venue.code = ?
				AND lower(symbol_row.market_type) = ?
				AND market.is_active = TRUE
				AND symbol_row.is_active = TRUE`,
				provider, marketType).
			Group("symbol_row.base_asset_guid").
			Pluck("symbol_row.base_asset_guid", &ids).Error; err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		if _, ok := allowed[id]; ok {
			result[id] = struct{}{}
		}
	}
	return result, nil
}

func (m *marketAggregationDB) QueryAssetIndexDashboard(query AssetIndexDashboardQuery) ([]AssetIndexDashboardRow, int64, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 50
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	venue, priceKind, err := NormalizeDashboardVenue(query.Venue)
	if err != nil {
		return nil, 0, err
	}
	display := dashboardDisplay(venue)
	venueStatisticFresh := "venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes'"
	if venue == "uniswap" || venue == "pancakeswap" {
		venueStatisticFresh = display.dexRoute
	}
	published := map[string]struct{}{}
	eligible := map[string]struct{}{}
	if venue != "all" {
		published, err = m.queryPublishedProviderAssetIDs(venue)
		if err != nil {
			return nil, 0, err
		}
		eligibleIDs, eligibleErr := m.QueryEligibleProviderAssetIDs(venue, 50)
		if eligibleErr != nil {
			return nil, 0, eligibleErr
		}
		for _, id := range eligibleIDs {
			eligible[id] = struct{}{}
		}
	}
	selectionQuery := m.gorm.Table("asset_metric_current metric").
		Select(`metric.asset_guid,
			0::bigint AS selection_version,
			metric.market_cap_rank AS selection_rank`).
		Where("metric.market_cap_rank BETWEEN 1 AND 50")
	if venue == "all" {
		selectionQuery = m.gorm.Table("provider_asset_selection selection").
			Select(`selection.asset_guid,
				MAX(selection.selection_version)::bigint AS selection_version,
				MIN(selection.selection_rank)::integer AS selection_rank`).
			Joins(`JOIN provider_asset_selection_state selection_state
				ON selection_state.provider = selection.provider
				AND selection_state.active_version = selection.selection_version`).
			Where("selection.provider IN ?", providerSelectionUniverse()).
			Group("selection.asset_guid")
	} else {
		selectionQuery = m.gorm.Table("provider_asset_selection selection").
			Select(`selection.asset_guid,
				selection.selection_version,
				selection.selection_rank`).
			Joins(`JOIN provider_asset_selection_state selection_state
				ON selection_state.provider = selection.provider
				AND selection_state.active_version = selection.selection_version`).
			Where("selection.provider = ?", venue)
	}
	base := m.gorm.Table("(?) selected", selectionQuery).
		Joins("JOIN asset_metric_current am ON am.asset_guid = selected.asset_guid").
		Joins("JOIN asset a ON a.guid = am.asset_guid").
		Joins(`LEFT JOIN asset_venue_snapshot venue_snapshot
			ON venue_snapshot.asset_guid = am.asset_guid
			AND venue_snapshot.provider = ?
			AND venue_snapshot.price_kind = ?`, venue, priceKind).
		Joins(`LEFT JOIN asset_venue_snapshot latest_snapshot
			ON latest_snapshot.asset_guid = am.asset_guid
			AND latest_snapshot.provider = ?
			AND latest_snapshot.price_kind = ?`, venue, priceKind).
		Joins(`LEFT JOIN asset_price_index composite
			ON composite.asset_guid = am.asset_guid
			AND composite.available = TRUE
			AND composite.observed_at >= clock_timestamp() - INTERVAL '30 seconds'`).
		Where("am.market_cap_rank BETWEEN 1 AND 200")
	if venue != "all" && !query.IncludeUncovered {
		publishedIDs := make([]string, 0, len(published))
		for assetID := range published {
			publishedIDs = append(publishedIDs, assetID)
		}
		if len(publishedIDs) == 0 {
			base = base.Where("1 = 0")
		} else {
			base = base.Where("am.asset_guid IN ?", publishedIDs)
		}
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		base = base.Where("(a.guid = ? OR a.asset_symbol ILIKE ? OR a.asset_name ILIKE ?)", search, like, like)
	}
	switch strings.ToLower(strings.TrimSpace(query.Filter)) {
	case "gainers":
		base = base.Where("(" + display.change + ") > 0")
	case "losers":
		base = base.Where("(" + display.change + ") < 0")
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AssetIndexDashboardRow
	selectSQL := `am.asset_guid AS asset_id,
			a.asset_symbol,
			a.asset_name,
			COALESCE(NULLIF(am.image_url, ''), a.asset_logo) AS logo,
			am.market_cap_rank AS rank,
			selected.selection_version,
			selected.selection_rank,
			CASE
				WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes'
				THEN venue_snapshot.price_usd
			END AS price,
			composite.price_usd AS composite_price,
			CASE
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN am.reference_price_usd
			END AS market_reference_price,
			` + display.price + ` AS display_price,
			` + display.priceKind + ` AS display_price_kind,
			` + display.change + ` AS display_change_24h_pct,
			` + display.changeKind + ` AS display_change_kind,
			(` + display.available + `) AS display_available,
			` + display.observedAt + ` AS display_observed_at,
			(` + display.dexRoute + `) AS dex_route_available,
			CASE
				WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes'
				THEN venue_snapshot.change_24h_pct
			END AS change_24h_pct,
			COALESCE(venue_snapshot.version, 0) AS venue_price_version,
			composite.change_24h_pct AS composite_change_24h_pct,
			composite.turnover_24h_usd AS composite_turnover_24h_usd,
			COALESCE(composite.contributor_count, 0) AS composite_contributor_count,
			COALESCE(composite.confidence, 'unknown') AS composite_confidence,
			COALESCE(composite.contributors, '[]'::jsonb) AS composite_contributors,
			composite.observed_at AS composite_observed_at,
			COALESCE(composite.version, 0) AS composite_version,
			CASE
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN am.observed_at
			END AS reference_observed_at,
			CASE
				WHEN am.reference_price_usd IS NOT NULL
				  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
				THEN am.provider_updated_at
			END AS reference_source_time,
			am.market_cap_usd,
			CASE
				WHEN ` + venueStatisticFresh + `
				THEN venue_snapshot.turnover_24h_usd
			END AS turnover_24h_usd,
			am.circulating_supply,
			(
				SELECT COUNT(*)
				FROM symbol market_symbol
				JOIN exchange_symbol market ON market.symbol_guid = market_symbol.guid
				JOIN exchange market_venue ON market_venue.guid = market.exchange_guid
				JOIN provider_rollout_state market_rollout
				  ON market_rollout.provider = market_venue.code
				WHERE market_symbol.base_asset_guid = am.asset_guid
				  AND market_symbol.is_active = TRUE
				  AND market.is_active = TRUE
				  AND lower(market_symbol.market_type) = 'spot'
				  AND (
				  market_rollout.local_preview_enabled = TRUE
				  OR
				  market_rollout.mode = 'enabled'
				  OR (
				  market_rollout.mode = 'canary'
				  AND market_rollout.canary_asset_ids @> jsonb_build_array(am.asset_guid)
				  )
				  )
				  AND (? = 'all' OR market_venue.code = ?)
			) AS spot_market_count,
			(
				SELECT COUNT(*)
				FROM symbol market_symbol
				JOIN exchange_symbol market ON market.symbol_guid = market_symbol.guid
				JOIN exchange market_venue ON market_venue.guid = market.exchange_guid
				JOIN provider_rollout_state market_rollout
				  ON market_rollout.provider = market_venue.code
				WHERE market_symbol.base_asset_guid = am.asset_guid
				  AND market_symbol.is_active = TRUE
				  AND market.is_active = TRUE
				  AND lower(market_symbol.market_type) = 'perp'
				  AND (
				  market_rollout.local_preview_enabled = TRUE
				  OR
				  market_rollout.mode = 'enabled'
				  OR (
				  market_rollout.mode = 'canary'
				  AND market_rollout.canary_asset_ids @> jsonb_build_array(am.asset_guid)
				  )
				  )
				  AND (? = 'all' OR market_venue.code = ?)
			) AS perp_market_count,
			(
				SELECT COUNT(*)
				FROM dex_route_current route
				JOIN provider_rollout_state route_rollout
				  ON route_rollout.provider = route.provider
				WHERE route.asset_guid = am.asset_guid
				  AND (? = 'all' OR route.provider = ?)
				  AND route.available = TRUE
				  AND (
				  route_rollout.local_preview_enabled = TRUE
				  OR
				  route_rollout.mode = 'enabled'
				  OR (
				  route_rollout.mode = 'canary'
				  AND route_rollout.canary_asset_ids @> jsonb_build_array(am.asset_guid)
				  )
				  )
				  AND EXISTS (
				  SELECT 1
				  FROM asset_venue_snapshot published_route
				  WHERE published_route.asset_guid = route.asset_guid
				  AND published_route.provider = route.provider
				  AND published_route.price_kind = 'dex_route'
				  AND published_route.available = TRUE
				  AND published_route.last_success_at >= clock_timestamp() - INTERVAL '60 seconds'
				  )
			) AS dex_route_count,
			CASE WHEN ` + venueStatisticFresh + `
				THEN COALESCE(venue_snapshot.contributor_count, 0) ELSE 0
			END AS contributor_count,
			CASE WHEN ` + venueStatisticFresh + `
				THEN COALESCE(venue_snapshot.contributor_count, 0) ELSE 0
			END AS priced_venue_count,
			COALESCE(venue_snapshot.confidence, 'unknown') AS confidence,
			COALESCE(venue_snapshot.quality, 'unknown') AS quality,
			COALESCE(venue_snapshot.price_kind, ?) AS price_kind,
			? AS price_source,
			''::text AS coverage_status,
			''::text AS coverage_reason,
			CASE
				WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes'
				THEN TRUE
				ELSE FALSE
			END AS available,
			venue_snapshot.source_time,
			venue_snapshot.last_success_at AS observed_at,
			am.provider_updated_at,
			(latest_snapshot.last_success_at IS NOT NULL) AS latest_available,
			latest_snapshot.last_success_at AS latest_observed_at,
			venue_snapshot.last_attempt_at,
			venue_snapshot.last_success_at,
			venue_snapshot.last_error_class,
			CASE
				WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '30 seconds' THEN 'fresh'
				WHEN venue_snapshot.last_success_at >= clock_timestamp() - INTERVAL '5 minutes' THEN 'stale'
				ELSE 'unavailable'
			END AS freshness_status,
			CASE
				WHEN venue_snapshot.last_success_at IS NULL THEN NULL
				ELSE GREATEST(0, EXTRACT(EPOCH FROM (clock_timestamp() - venue_snapshot.last_success_at)))::bigint
			END AS freshness_age_seconds`
	err = base.Select(selectSQL,
		venue, venue, venue, venue, venue, venue,
		priceKind, venue).
		Order(assetDashboardOrder(query.SortBy, query.SortDirection, display)).
		Limit(int(query.PageSize)).
		Offset(int((query.Page - 1) * query.PageSize)).
		Scan(&rows).Error
	for index := range rows {
		row := &rows[index]
		switch {
		case (venue == "uniswap" || venue == "pancakeswap") &&
			row.DisplayAvailable && !row.DexRouteAvailable:
			row.CoverageStatus = "reference_only"
			row.CoverageReason = "no_current_route"
		case row.FreshnessStatus == "fresh":
			row.CoverageStatus = "covered"
			if row.Change24hPct == nil {
				row.CoverageReason = "missing_24h_reference"
			}
		case row.FreshnessStatus == "stale":
			row.CoverageStatus = "stale"
			row.CoverageReason = "stale"
		case venue == "all":
			if row.SpotMarketCount > 0 {
				row.CoverageStatus = "source_unavailable"
				row.CoverageReason = "source_unavailable"
			} else {
				row.CoverageStatus = "not_covered"
				row.CoverageReason = "not_covered"
			}
		default:
			if _, ok := published[row.AssetID]; ok {
				row.CoverageStatus = "source_unavailable"
				row.CoverageReason = "source_unavailable"
			} else if _, ok := eligible[row.AssetID]; ok {
				row.CoverageStatus = "rollout_pending"
				row.CoverageReason = "rollout_pending"
			} else {
				row.CoverageStatus = "not_listed"
				row.CoverageReason = "not_listed"
			}
		}
	}
	return rows, total, err
}

func (m *marketAggregationDB) QueryMarketPriceTicks(query MarketPriceTickQuery) ([]MarketPriceTickRow, error) {
	venue, priceKind, err := NormalizeDashboardVenue(query.Venue)
	if err != nil {
		return nil, err
	}
	assetIDs := make([]string, 0, len(query.AssetIDs))
	seen := make(map[string]struct{}, len(query.AssetIDs))
	for _, candidate := range query.AssetIDs {
		assetID := strings.TrimSpace(candidate)
		if assetID == "" {
			continue
		}
		if _, exists := seen[assetID]; exists {
			continue
		}
		seen[assetID] = struct{}{}
		assetIDs = append(assetIDs, assetID)
	}
	if len(assetIDs) == 0 {
		return []MarketPriceTickRow{}, nil
	}

	var rows []MarketPriceTickRow
	if venue == "all" {
		err = m.gorm.Table("asset_price_index price").
			Select(`price.asset_guid AS asset_id,
				'all' AS provider,
				'composite_spot' AS price_kind,
				price.price_usd,
				price.change_24h_pct,
				price.turnover_24h_usd,
				price.contributor_count,
				price.confidence,
				price.confidence AS quality,
				COALESCE(price.contributors, '[]'::jsonb) AS contributors,
				(
					price.available = TRUE
					AND price.price_usd IS NOT NULL
					AND price.observed_at >= clock_timestamp() - INTERVAL '30 seconds'
				) AS available,
				price.observed_at AS source_time,
				price.observed_at,
				price.observed_at AS last_success_at,
				price.version`).
			Where("price.asset_guid IN ?", assetIDs).
			Order("price.asset_guid ASC").
			Scan(&rows).Error
		return rows, err
	}

	freshInterval := "30 seconds"
	if priceKind == "dex_route" {
		freshInterval = "60 seconds"
	}
	err = m.gorm.Table("asset_venue_snapshot snapshot").
		Select(`snapshot.asset_guid AS asset_id,
			snapshot.provider,
			snapshot.price_kind,
			snapshot.price_usd,
			snapshot.change_24h_pct,
			snapshot.turnover_24h_usd,
			snapshot.contributor_count,
			snapshot.confidence,
			snapshot.quality,
			'[]'::jsonb AS contributors,
			(
				snapshot.available = TRUE
				AND snapshot.price_usd IS NOT NULL
				AND snapshot.last_success_at >= clock_timestamp() - (?::interval)
			) AS available,
			snapshot.source_time,
			snapshot.observed_at,
			snapshot.last_success_at,
			snapshot.version`, freshInterval).
		Where(`snapshot.provider = ?
			AND snapshot.price_kind = ?
			AND snapshot.asset_guid IN ?`, venue, priceKind, assetIDs).
		Order("snapshot.asset_guid ASC").
		Scan(&rows).Error
	return rows, err
}

func NormalizeDashboardVenue(value string) (string, string, error) {
	switch normalizedProvider(value) {
	case "", "all":
		return "all", "composite_spot", nil
	case "binance", "coinbase", "bybit", "okx":
		return normalizedProvider(value), "venue_spot", nil
	case "hyperliquid":
		return "hyperliquid", "perp_mark", nil
	case "uniswap", "pancakeswap":
		return normalizedProvider(value), "dex_route", nil
	default:
		return "", "", fmt.Errorf("%w: %q", ErrInvalidDashboardVenue, value)
	}
}

func (m *marketAggregationDB) QueryAssetMarkets(assetID string) ([]AssetMarketReadRow, error) {
	var rows []AssetMarketReadRow
	err := m.gorm.Table("exchange_symbol es").
		Select(`es.guid AS market_id,
			es.market_code,
			e.code AS provider,
			s.symbol_name AS symbol,
			lower(s.market_type) AS market_type,
			upper(qa.asset_symbol) AS quote_asset,
			sm.price,
			sm.change_24h_pct,
			COALESCE(sm.quote_turnover_usd, sm.quote_turnover_24h, sm.volume) AS turnover_24h,
			sm.observed_at,
			sm.source_time,
			sm.source_time_kind,
			EXISTS(SELECT 1 FROM symbol_kline sk WHERE sk.market_id = es.guid LIMIT 1) AS has_kline,
				EXISTS(
					SELECT 1
					FROM asset_price_index api,
					     jsonb_array_elements(
					     CASE
					     WHEN jsonb_typeof(api.contributors) = 'array' THEN api.contributors
					     ELSE '[]'::jsonb
					     END
					     ) contributor
				WHERE api.asset_guid = s.base_asset_guid
				  AND contributor ->> 'market_id' = es.guid
			) AS composite_contributor`).
		Joins("JOIN exchange e ON e.guid = es.exchange_guid").
		Joins("JOIN symbol s ON s.guid = es.symbol_guid").
		Joins("JOIN asset qa ON qa.guid = s.qoute_asset_guid").
		Joins("LEFT JOIN symbol_market sm ON sm.market_id = es.guid").
		Where("s.base_asset_guid = ? AND es.is_active = TRUE AND s.is_active = TRUE", assetID).
		Order("CASE WHEN lower(s.market_type) = 'spot' THEN 0 ELSE 1 END, e.code ASC, es.guid ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	allowedByProvider := make(map[string]map[string]struct{})
	result := make([]AssetMarketReadRow, 0, len(rows))
	for _, row := range rows {
		allowed, loaded := allowedByProvider[row.Provider]
		if !loaded {
			allowed, _, err = m.QueryPublishedAssetIDs(row.Provider)
			if err != nil {
				return nil, err
			}
			allowedByProvider[row.Provider] = allowed
		}
		if _, ok := allowed[assetID]; ok {
			result = append(result, row)
		}
	}
	return result, nil
}

func (m *marketAggregationDB) QueryCatalogAudit(provider, status string, rankLimit int, page, pageSize int64) ([]CatalogAuditRow, []CatalogAuditCount, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 250 {
		pageSize = 250
	}
	provider = normalizedProvider(provider)
	status = strings.ToLower(strings.TrimSpace(status))

	const auditCTE = `WITH catalog_audit AS (
		SELECT
			candidate.provider,
			candidate.source_symbol,
			candidate.market_type,
			candidate.base_alias,
			candidate.quote_alias,
			candidate.upstream_status,
			candidate.resolution_status,
			candidate.base_asset_guid,
			candidate.quote_asset_guid,
			candidate.rejection_reason,
			candidate.last_seen_at,
			metric.market_cap_rank AS rank,
			'cex_market'::text AS candidate_kind,
			COALESCE(alias.review_status, 'missing') AS alias_review,
			COALESCE(rollout.mode, 'unconfigured') AS rollout_mode,
			COALESCE(alias.review_source, '') AS resolution_source
		FROM provider_market_candidate candidate
		LEFT JOIN asset_alias alias
			ON alias.provider = candidate.provider
			AND alias.alias = upper(candidate.base_alias)
		LEFT JOIN asset_metric_current metric
			ON metric.asset_guid = COALESCE(candidate.base_asset_guid, alias.asset_guid)
		LEFT JOIN provider_rollout_state rollout
			ON rollout.provider = candidate.provider

		UNION ALL

		SELECT
			pool.provider,
			pool.pool_address AS source_symbol,
			'v3_pool'::text AS market_type,
			pool.token0_address AS base_alias,
			pool.token1_address AS quote_alias,
			'onchain'::text AS upstream_status,
			pool.resolution_status,
			token0.asset_guid AS base_asset_guid,
			token1.asset_guid AS quote_asset_guid,
			pool.rejection_reason,
			pool.last_seen_at,
			CASE
				WHEN metric0.market_cap_rank IS NULL THEN metric1.market_cap_rank
				WHEN metric1.market_cap_rank IS NULL THEN metric0.market_cap_rank
				ELSE LEAST(metric0.market_cap_rank, metric1.market_cap_rank)
			END AS rank,
			'dex_pool'::text AS candidate_kind,
			CASE
				WHEN token0.review_status = 'approved' AND token1.review_status = 'approved' THEN 'approved'
				ELSE 'missing'
			END AS alias_review,
			COALESCE(rollout.mode, 'unconfigured') AS rollout_mode,
			concat_ws(' + ', token0.review_source, token1.review_source) AS resolution_source
		FROM dex_pool_candidate pool
		LEFT JOIN asset_representation token0
			ON token0.chain_id = pool.chain_id
			AND token0.contract_address = pool.token0_address
		LEFT JOIN asset_representation token1
			ON token1.chain_id = pool.chain_id
			AND token1.contract_address = pool.token1_address
		LEFT JOIN asset_metric_current metric0 ON metric0.asset_guid = token0.asset_guid
		LEFT JOIN asset_metric_current metric1 ON metric1.asset_guid = token1.asset_guid
		LEFT JOIN provider_rollout_state rollout ON rollout.provider = pool.provider
	)`
	const filter = `
		WHERE (? = '' OR provider = ?)
		  AND (? = '' OR resolution_status = ?)
		  AND (? <= 0 OR rank BETWEEN 1 AND ?)`

	var total int64
	if err := m.gorm.Raw(
		auditCTE+" SELECT COUNT(*) FROM catalog_audit"+filter,
		provider, provider, status, status, rankLimit, rankLimit,
	).Scan(&total).Error; err != nil {
		return nil, nil, 0, err
	}
	var rows []CatalogAuditRow
	if err := m.gorm.Raw(
		auditCTE+` SELECT * FROM catalog_audit`+filter+`
			ORDER BY provider ASC, resolution_status ASC, rank ASC NULLS LAST, source_symbol ASC
			LIMIT ? OFFSET ?`,
		provider, provider, status, status, rankLimit, rankLimit,
		pageSize, (page-1)*pageSize,
	).Scan(&rows).Error; err != nil {
		return nil, nil, 0, err
	}
	var counts []CatalogAuditCount
	if err := m.gorm.Raw(
		auditCTE+` SELECT resolution_status AS status, COUNT(*) AS count
			FROM catalog_audit
			WHERE (? = '' OR provider = ?)
			  AND (? <= 0 OR rank BETWEEN 1 AND ?)
			GROUP BY resolution_status
			ORDER BY resolution_status ASC`,
		provider, provider, rankLimit, rankLimit,
	).Scan(&counts).Error; err != nil {
		return nil, nil, 0, err
	}
	return rows, counts, total, nil
}

func (m *marketAggregationDB) QueryGlobalMetric(provider string) (*MarketGlobalMetric, error) {
	var row MarketGlobalMetric
	result := m.gorm.Where("provider = ?", normalizedProvider(provider)).First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &row, nil
}

func (m *marketAggregationDB) QueryAssetPriceIndex(assetID string) (*AssetPriceIndex, error) {
	var row AssetPriceIndex
	result := m.gorm.Where("asset_guid = ?", strings.TrimSpace(assetID)).First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, result.Error
}

func (m *marketAggregationDB) QueryAssetIndexSummary(venue string) (*AssetIndexSummary, error) {
	provider, priceKind, err := NormalizeDashboardVenue(venue)
	if err != nil {
		return nil, err
	}
	universeQuery := m.gorm.Table("asset_metric_current metric").
		Select("metric.asset_guid").
		Where("metric.market_cap_rank BETWEEN 1 AND 50")
	if provider == "all" {
		universeQuery = m.gorm.Table("provider_asset_selection selection").
			Select("selection.asset_guid").
			Joins(`JOIN provider_asset_selection_state selection_state
				ON selection_state.provider = selection.provider
				AND selection_state.active_version = selection.selection_version`).
			Where("selection.provider IN ?", providerSelectionUniverse()).
			Group("selection.asset_guid")
	} else {
		universeQuery = m.gorm.Table("provider_asset_selection selection").
			Select("selection.asset_guid").
			Joins(`JOIN provider_asset_selection_state selection_state
				ON selection_state.provider = selection.provider
				AND selection_state.active_version = selection.selection_version`).
			Where("selection.provider = ?", provider)
	}
	var universeCount int64
	if err := m.gorm.Table("(?) universe", universeQuery).
		Count(&universeCount).Error; err != nil {
		return nil, err
	}
	eligibleCount := universeCount
	publishedCount := universeCount
	localPreviewEnabled := false
	if provider != "all" {
		_, selectionState, selectionErr := m.QueryProviderSelectedAssetIDs(provider)
		if selectionErr != nil {
			return nil, selectionErr
		}
		if selectionState != nil {
			eligibleCount = int64(selectionState.CandidateCount)
			publishedCount = int64(selectionState.SelectedCount)
		} else {
			eligible, eligibleErr := m.QueryEligibleProviderAssetIDs(provider, 50)
			if eligibleErr != nil {
				return nil, eligibleErr
			}
			published, publishedErr := m.queryPublishedProviderAssetIDs(provider)
			if publishedErr != nil {
				return nil, publishedErr
			}
			eligibleCount = int64(len(eligible))
			publishedCount = int64(len(published))
		}
		rollout, rolloutErr := m.QueryProviderRollout(provider)
		if rolloutErr != nil {
			return nil, rolloutErr
		}
		if rollout != nil && rollout.LocalPreviewEnabled {
			localPreviewEnabled = true
		}
	}
	isDex := provider == "uniswap" || provider == "pancakeswap"
	routeFreshCondition := "snapshot.last_success_at >= clock_timestamp() - INTERVAL '30 seconds'"
	displayPriceCondition := routeFreshCondition
	displayChange := "CASE WHEN " + routeFreshCondition + " THEN snapshot.change_24h_pct END"
	referenceOnlyCondition := "FALSE"
	displayObservedAt := "CASE WHEN " + routeFreshCondition + " THEN snapshot.last_success_at END"
	if isDex {
		routeFreshCondition = "snapshot.last_success_at >= clock_timestamp() - INTERVAL '60 seconds' AND snapshot.price_usd IS NOT NULL"
		displayPriceCondition = `(` + routeFreshCondition + `)
			OR (composite.available = TRUE
				AND composite.observed_at >= clock_timestamp() - INTERVAL '30 seconds'
				AND composite.price_usd IS NOT NULL)
			OR (am.reference_price_usd IS NOT NULL
				AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes')`
		displayChange = `CASE
			WHEN ` + routeFreshCondition + ` AND snapshot.change_24h_pct IS NOT NULL
			THEN snapshot.change_24h_pct
			WHEN composite.available = TRUE
			  AND composite.observed_at >= clock_timestamp() - INTERVAL '30 seconds'
			THEN composite.change_24h_pct
		END`
		referenceOnlyCondition = `(NOT (` + routeFreshCondition + `)) AND (` + displayPriceCondition + `)`
		displayObservedAt = `CASE
			WHEN ` + routeFreshCondition + ` THEN snapshot.last_success_at
			WHEN composite.available = TRUE
			  AND composite.observed_at >= clock_timestamp() - INTERVAL '30 seconds'
			THEN composite.observed_at
			WHEN am.reference_price_usd IS NOT NULL
			  AND am.observed_at >= clock_timestamp() - INTERVAL '15 minutes'
			THEN am.observed_at
		END`
	}
	var row AssetIndexSummary
	summarySelect := `COUNT(*) AS asset_count,
			COUNT(*) FILTER (WHERE ` + routeFreshCondition + `) AS priced_asset_count,
			COUNT(*) FILTER (WHERE ` + displayPriceCondition + `) AS displayed_asset_count,
			COUNT(*) FILTER (WHERE ` + routeFreshCondition + `) AS routable_asset_count,
			COUNT(*) FILTER (WHERE ` + referenceOnlyCondition + `) AS reference_only_asset_count,
			COUNT(*) FILTER (WHERE NOT (` + displayPriceCondition + `)) AS unpriced_asset_count,
			COUNT(*) FILTER (WHERE ` + routeFreshCondition + ` AND snapshot.contributor_count = 1) AS single_venue_priced_asset_count,
			COUNT(*) FILTER (WHERE ` + routeFreshCondition + ` AND snapshot.contributor_count >= 2) AS multi_venue_priced_asset_count,
			COUNT(*) FILTER (WHERE (` + displayChange + `) IS NOT NULL) AS change_available_count,
			COUNT(*) FILTER (WHERE (` + displayChange + `) > 0) AS advancers,
			COUNT(*) FILTER (WHERE (` + displayChange + `) < 0) AS decliners,
			COUNT(*) FILTER (WHERE (` + displayChange + `) = 0) AS flat,
			COUNT(*) FILTER (WHERE (` + displayChange + `) IS NULL) AS unknown,
			COALESCE(SUM(snapshot.turnover_24h_usd) FILTER (WHERE ` + routeFreshCondition + `), 0) AS covered_volume,
			MAX(` + displayObservedAt + `) AS observed_at`
	base := m.gorm.Table("(?) universe", universeQuery).
		Joins("JOIN asset_metric_current am ON am.asset_guid = universe.asset_guid").
		Joins("LEFT JOIN asset_price_index composite ON composite.asset_guid = am.asset_guid").
		Select(summarySelect).
		Joins(`LEFT JOIN asset_venue_snapshot snapshot
			ON snapshot.asset_guid = am.asset_guid
			AND snapshot.provider = ?
			AND snapshot.price_kind = ?`, provider, priceKind)
	err = base.Scan(&row).Error
	if err != nil {
		return nil, err
	}
	row.Top50UniverseCount = universeCount
	row.EligibleAssetCount = eligibleCount
	row.PublishedAssetCount = publishedCount
	if localPreviewEnabled {
		row.LocalPreviewEnabled = true
		_, _, row.PreviewSourceKey = providerPreviewKeys(provider)
		row.PreviewCoveredCount = row.PricedAssetCount
	}
	if provider == "all" {
		if err := m.gorm.Raw(`
			SELECT COUNT(DISTINCT contributor->>'provider')
			FROM asset_price_index idx
			CROSS JOIN LATERAL jsonb_array_elements(idx.contributors) contributor
			JOIN (
				SELECT selection.asset_guid
				FROM provider_asset_selection selection
					JOIN provider_asset_selection_state state
					  ON state.provider = selection.provider
					 AND state.active_version = selection.selection_version
					WHERE selection.provider IN (
						'binance', 'coinbase', 'bybit', 'okx',
						'hyperliquid', 'uniswap', 'pancakeswap'
					)
					GROUP BY selection.asset_guid
			) universe ON universe.asset_guid = idx.asset_guid
			WHERE TRUE
			  AND idx.available = TRUE
			  AND idx.observed_at >= clock_timestamp() - INTERVAL '30 seconds'
		`).Scan(&row.ContributingProviderCount).Error; err != nil {
			return nil, err
		}
	} else if row.PricedAssetCount > 0 {
		row.ContributingProviderCount = 1
	}
	return &row, nil
}

func (m *marketAggregationDB) EnableResolvedSpotMarkets(
	provider string,
	catalogObservedAt time.Time,
	allowedAssetIDs map[string]struct{},
) (int64, error) {
	provider = normalizedProvider(provider)
	var enabled int64
	err := m.gorm.Transaction(func(tx *gorm.DB) error {
		var exchange struct {
			Guid string `gorm:"column:guid"`
		}
		if err := tx.Table("exchange").Select("guid").Where("code = ?", provider).Take(&exchange).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		// A provider-managed market is deactivated only when the current,
		// successfully decoded catalog explicitly reports it as ineligible.
		// Absence from one response is not enough evidence to delist it.
		if err := tx.Exec(`
			UPDATE exchange_symbol es
			SET is_active = FALSE,
			    updated_at = ?
			FROM provider_market_candidate candidate
			WHERE es.exchange_guid = ?
			  AND candidate.provider = ?
			  AND candidate.market_type = 'spot'
			  AND candidate.source_symbol = es.source_symbol
			  AND candidate.last_seen_at >= ?
			  AND candidate.resolution_status NOT IN ('resolved', 'enabled')`,
			now, exchange.Guid, provider, catalogObservedAt,
		).Error; err != nil {
			return err
		}
		allowed := make([]string, 0, len(allowedAssetIDs))
		for assetID := range allowedAssetIDs {
			allowed = append(allowed, assetID)
		}
		disableQuery := `
			UPDATE exchange_symbol market
			SET is_active = FALSE,
			    updated_at = ?
			FROM symbol symbol_row
			WHERE market.symbol_guid = symbol_row.guid
			  AND market.exchange_guid = ?
			  AND market.guid LIKE ?`
		disableArgs := []interface{}{now, exchange.Guid, "provider-market:" + provider + ":%"}
		if len(allowed) > 0 {
			disableQuery += " AND symbol_row.base_asset_guid NOT IN ?"
			disableArgs = append(disableArgs, allowed)
		}
		if err := tx.Exec(disableQuery, disableArgs...).Error; err != nil {
			return err
		}
		if len(allowed) == 0 {
			return nil
		}
		var candidates []ProviderMarketCandidate
		if err := tx.Where(
			"provider = ? AND market_type = 'spot' AND resolution_status IN ('resolved', 'enabled') AND last_seen_at >= ? AND base_asset_guid IN ?",
			provider, catalogObservedAt, allowed,
		).
			Order("source_symbol ASC").Find(&candidates).Error; err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.BaseAssetGuid == nil || candidate.QuoteAssetGuid == nil {
				continue
			}
			symbolID := "provider-symbol:" + provider + ":" + strings.ToLower(candidate.SourceSymbol)
			marketID := "provider-market:" + provider + ":" + strings.ToLower(candidate.SourceSymbol)
			var existing struct {
				Guid       string `gorm:"column:guid"`
				SymbolGuid string `gorm:"column:symbol_guid"`
			}
			existingResult := tx.Table("exchange_symbol").
				Select("guid, symbol_guid").
				Where("exchange_guid = ? AND source_symbol = ?", exchange.Guid, candidate.SourceSymbol).
				Order("is_active DESC, guid ASC").
				Take(&existing)
			if existingResult.Error != nil && existingResult.Error != gorm.ErrRecordNotFound {
				return existingResult.Error
			}
			if existingResult.Error == nil {
				// Preserve the stable market_id of reviewed legacy markets
				// (notably the original six Binance pairs) instead of
				// colliding with their globally unique market_code.
				marketID = existing.Guid
				symbolID = existing.SymbolGuid
			}
			symbolName := strings.ToUpper(candidate.BaseAlias) + "/" + strings.ToUpper(candidate.QuoteAlias)
			if err := tx.Exec(`
				INSERT INTO symbol(guid, symbol_name, base_asset_guid, qoute_asset_guid, market_type, is_active, created_at, updated_at)
				VALUES (?, ?, ?, ?, 'spot', TRUE, ?, ?)
				ON CONFLICT (guid) DO UPDATE
				SET symbol_name = EXCLUDED.symbol_name,
				    base_asset_guid = EXCLUDED.base_asset_guid,
				    qoute_asset_guid = EXCLUDED.qoute_asset_guid,
				    is_active = TRUE,
				    updated_at = EXCLUDED.updated_at`,
				symbolID, symbolName, *candidate.BaseAssetGuid, *candidate.QuoteAssetGuid, now, now,
			).Error; err != nil {
				return err
			}
			marketCode := provider + ":" + symbolName + ":spot"
			if err := tx.Exec(`
				INSERT INTO exchange_symbol(
					guid, exchange_guid, symbol_guid, price, ask_price, bid_price,
					volume, radio, is_active, market_code, source_symbol, created_at, updated_at
				)
				VALUES (?, ?, ?, 0, 0, 0, 0, 0, TRUE, ?, ?, ?, ?)
				ON CONFLICT (guid) DO UPDATE
				SET market_code = EXCLUDED.market_code,
				    source_symbol = EXCLUDED.source_symbol,
				    is_active = TRUE,
				    updated_at = EXCLUDED.updated_at`,
				marketID, exchange.Guid, symbolID, marketCode, candidate.SourceSymbol, now, now,
			).Error; err != nil {
				return err
			}
			if err := tx.Model(&ProviderMarketCandidate{}).
				Where("provider = ? AND source_symbol = ? AND market_type = 'spot'", provider, candidate.SourceSymbol).
				Updates(map[string]interface{}{"resolution_status": "enabled", "enabled_at": now}).Error; err != nil {
				return err
			}
			enabled++
		}
		return nil
	})
	return enabled, err
}
