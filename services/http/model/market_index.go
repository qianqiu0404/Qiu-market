package model

type MarketOverviewRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Venue         string `json:"venue"`
	Universe      string `json:"universe"`
	SnapshotID    string `json:"snapshot_id"`
}

type AvailableDecimal struct {
	Value     *string `json:"value"`
	Available bool    `json:"available"`
}

// MarketPriceFact is the auditable price boundary shared by the dashboard and
// lightweight tick endpoint. Value availability, provenance, time, freshness,
// and quality travel together so clients never have to infer one from another.
type MarketPriceFact struct {
	PriceUSD            AvailableDecimal `json:"price_usd"`
	Change24hPct        AvailableDecimal `json:"change_24h_pct"`
	Turnover24hUSD      AvailableDecimal `json:"turnover_24h_usd"`
	Available           bool             `json:"available"`
	Kind                string           `json:"kind"`
	Source              string           `json:"source"`
	SourceTime          int64            `json:"source_time"`
	ObservedAt          int64            `json:"observed_at"`
	LastSuccessAt       int64            `json:"last_success_at"`
	FreshnessStatus     string           `json:"freshness_status"`
	FreshnessAgeSeconds int64            `json:"freshness_age_seconds"`
	Quality             string           `json:"quality"`
	ContributorCount    int              `json:"contributor_count"`
	Contributors        []string         `json:"contributors"`
	Version             int64            `json:"version"`
}

type MarketOverviewResult struct {
	GlobalMarketCapUSD          AvailableDecimal `json:"global_market_cap_usd"`
	CoveredSpotVolume           AvailableDecimal `json:"covered_spot_volume_24h_usd"`
	BTCDominancePct             AvailableDecimal `json:"btc_dominance_pct"`
	AssetCount                  int64            `json:"asset_count"`
	Advancers                   int64            `json:"advancers"`
	Decliners                   int64            `json:"decliners"`
	Flat                        int64            `json:"flat"`
	Unknown                     int64            `json:"unknown"`
	AdvanceRatioPct             AvailableDecimal `json:"advance_ratio_pct"`
	ProviderUpdatedAt           int64            `json:"provider_updated_at"`
	IndexUpdatedAt              int64            `json:"index_updated_at"`
	Venue                       string           `json:"venue"`
	RankedAssetCount            int64            `json:"ranked_asset_count"`
	Top50UniverseCount          int64            `json:"top50_universe_count"`
	EligibleAssetCount          int64            `json:"eligible_asset_count"`
	PublishedAssetCount         int64            `json:"published_asset_count"`
	PricedAssetCount            int64            `json:"priced_asset_count"`
	DisplayedAssetCount         int64            `json:"displayed_asset_count"`
	RoutableAssetCount          int64            `json:"routable_asset_count"`
	ReferenceOnlyAssetCount     int64            `json:"reference_only_asset_count"`
	UnpricedAssetCount          int64            `json:"unpriced_asset_count"`
	FreshAssetCount             int64            `json:"fresh_asset_count"`
	StaleAssetCount             int64            `json:"stale_asset_count"`
	UnavailableAssetCount       int64            `json:"unavailable_asset_count"`
	ChangeAvailableCount        int64            `json:"change_available_count"`
	ContributingProviderCount   int64            `json:"contributing_provider_count"`
	SingleVenuePricedAssetCount int64            `json:"single_venue_priced_asset_count"`
	MultiVenuePricedAssetCount  int64            `json:"multi_venue_priced_asset_count"`
	CoverageRatioPct            AvailableDecimal `json:"coverage_ratio_pct"`
	DisplayCoverageRatioPct     AvailableDecimal `json:"display_coverage_ratio_pct"`
	LocalPreviewEnabled         bool             `json:"local_preview_enabled"`
	PreviewSourceKey            string           `json:"preview_source_key"`
	PreviewCoveredCount         int64            `json:"preview_covered_count"`
	Universe                    string           `json:"universe"`
	SelectionVersion            int64            `json:"selection_version"`
}

type MarketOverviewResponse struct {
	Code           uint64               `json:"code"`
	Message        string               `json:"message"`
	Result         MarketOverviewResult `json:"result"`
	SnapshotID     string               `json:"snapshot_id"`
	SnapshotAsOf   int64                `json:"snapshot_as_of"`
	SnapshotSchema string               `json:"snapshot_schema"`
}

type AssetDashboardV2Request struct {
	ConsumerToken    string `json:"consumer_token"`
	Page             int64  `json:"page"`
	PageSize         int64  `json:"page_size"`
	Venue            string `json:"venue"`
	Universe         string `json:"universe"`
	Search           string `json:"search"`
	Filter           string `json:"filter"`
	SortBy           string `json:"sort_by"`
	SortDirection    string `json:"sort_direction"`
	IncludeUncovered *bool  `json:"include_uncovered"`
	SnapshotID       string `json:"snapshot_id"`
}

type AssetDashboardV2Item struct {
	Rank                    *int             `json:"rank"`
	SelectionVersion        int64            `json:"selection_version"`
	SelectionRank           int              `json:"selection_rank"`
	AssetID                 string           `json:"asset_id"`
	AssetSymbol             string           `json:"asset_symbol"`
	AssetName               string           `json:"asset_name"`
	Logo                    string           `json:"logo"`
	PriceUSD                AvailableDecimal `json:"price_usd"`
	CompositePriceUSD       AvailableDecimal `json:"composite_price_usd"`
	MarketReferencePriceUSD AvailableDecimal `json:"market_reference_price_usd"`
	DisplayPriceUSD         AvailableDecimal `json:"display_price_usd"`
	DisplayPriceKind        string           `json:"display_price_kind"`
	DisplayChange24hPct     AvailableDecimal `json:"display_change_24h_pct"`
	DisplayChangeKind       string           `json:"display_change_kind"`
	DisplayAvailable        bool             `json:"display_available"`
	DisplayObservedAt       int64            `json:"display_observed_at"`
	DexRouteAvailable       bool             `json:"dex_route_available"`
	VenuePrice              MarketPriceFact  `json:"venue_price"`
	DexRoutePrice           MarketPriceFact  `json:"dex_route_price"`
	DisplayPrice            MarketPriceFact  `json:"display_price"`
	Change24hPct            AvailableDecimal `json:"change_24h_pct"`
	MarketCapUSD            AvailableDecimal `json:"market_cap_usd"`
	Turnover24hUSD          AvailableDecimal `json:"covered_turnover_24h_usd"`
	CirculatingSupply       AvailableDecimal `json:"circulating_supply"`
	SpotMarketCount         int64            `json:"spot_market_count"`
	PerpMarketCount         int64            `json:"perp_market_count"`
	DexRouteCount           int64            `json:"dex_route_count"`
	ContributorCount        int              `json:"contributor_count"`
	PricedVenueCount        int              `json:"priced_venue_count"`
	Confidence              string           `json:"confidence"`
	Quality                 string           `json:"quality"`
	PriceKind               string           `json:"price_kind"`
	PriceSource             string           `json:"price_source"`
	CoverageStatus          string           `json:"coverage_status"`
	CoverageReason          string           `json:"coverage_reason"`
	FreshnessStatus         string           `json:"freshness_status"`
	FreshnessAgeSeconds     int64            `json:"freshness_age_seconds"`
	LastAttemptAt           int64            `json:"last_attempt_at"`
	LastSuccessAt           int64            `json:"last_success_at"`
	LastErrorClass          string           `json:"last_error_class"`
	Available               bool             `json:"available"`
	SourceTime              int64            `json:"source_time"`
	ObservedAt              int64            `json:"observed_at"`
	IndexUpdatedAt          int64            `json:"index_updated_at"`
	ProviderUpdatedAt       int64            `json:"provider_updated_at"`
	SparklineAvailable      bool             `json:"sparkline_available"`
}

type AssetDashboardV2Response struct {
	Code           uint64                 `json:"code"`
	Message        string                 `json:"message"`
	Result         []AssetDashboardV2Item `json:"result"`
	Overview       MarketOverviewResult   `json:"overview"`
	Total          int64                  `json:"total"`
	Universe       string                 `json:"universe"`
	SnapshotID     string                 `json:"snapshot_id"`
	SnapshotAsOf   int64                  `json:"snapshot_as_of"`
	SnapshotSchema string                 `json:"snapshot_schema"`
}

type MarketPriceTicksRequest struct {
	ConsumerToken string   `json:"consumer_token"`
	Venue         string   `json:"venue"`
	AssetIDs      []string `json:"asset_ids"`
}

type MarketPriceTickItem struct {
	AssetID             string           `json:"asset_id"`
	Provider            string           `json:"provider"`
	PriceKind           string           `json:"price_kind"`
	PriceUSD            AvailableDecimal `json:"price_usd"`
	Change24hPct        AvailableDecimal `json:"change_24h_pct"`
	Turnover24hUSD      AvailableDecimal `json:"turnover_24h_usd"`
	VenuePrice          MarketPriceFact  `json:"venue_price"`
	DexRoutePrice       MarketPriceFact  `json:"dex_route_price"`
	DisplayPrice        MarketPriceFact  `json:"display_price"`
	Available           bool             `json:"available"`
	FreshnessStatus     string           `json:"freshness_status"`
	FreshnessAgeSeconds int64            `json:"freshness_age_seconds"`
	SourceTime          int64            `json:"source_time"`
	ObservedAt          int64            `json:"observed_at"`
	LastSuccessAt       int64            `json:"last_success_at"`
	Version             int64            `json:"version"`
}

type MarketPriceTicksResponse struct {
	Code       uint64                `json:"code"`
	Message    string                `json:"message"`
	Result     []MarketPriceTickItem `json:"result"`
	Venue      string                `json:"venue"`
	ServerTime int64                 `json:"server_time"`
}

type AssetMarketsRequest struct {
	ConsumerToken string `json:"consumer_token"`
	AssetID       string `json:"asset_id"`
	Venue         string `json:"venue"`
}

type AssetMarketV2Item struct {
	MarketID             string           `json:"market_id"`
	MarketCode           string           `json:"market_code"`
	Provider             string           `json:"provider"`
	Symbol               string           `json:"symbol"`
	MarketType           string           `json:"market_type"`
	QuoteAsset           string           `json:"quote_asset"`
	Price                AvailableDecimal `json:"price"`
	RelativeDeviationPct AvailableDecimal `json:"relative_deviation_pct"`
	Change24hPct         AvailableDecimal `json:"change_24h_pct"`
	Turnover24h          AvailableDecimal `json:"turnover_24h"`
	FreshnessStatus      string           `json:"freshness_status"`
	ProviderUpdatedAt    int64            `json:"provider_updated_at"`
	Confidence           string           `json:"confidence"`
	Quality              string           `json:"quality"`
	HasKline             bool             `json:"has_kline"`
	VenueKind            string           `json:"venue_kind"`
	Chain                string           `json:"chain"`
	Protocol             string           `json:"protocol"`
	RouteKey             string           `json:"route_key"`
	Route                []string         `json:"route"`
	PoolAddresses        []string         `json:"pool_addresses"`
	QuoteNotionalUSD     AvailableDecimal `json:"quote_notional_usd"`
	QuoteReferenceKind   string           `json:"quote_reference_kind"`
	TVLUSD               AvailableDecimal `json:"tvl_usd"`
	PriceImpactPct       AvailableDecimal `json:"price_impact_pct"`
	RoundTripSpreadPct   AvailableDecimal `json:"round_trip_spread_pct"`
	BlockNumber          int64            `json:"block_number"`
	BlockTimestamp       int64            `json:"block_timestamp"`
	Available            bool             `json:"available"`
	UnavailableReason    string           `json:"unavailable_reason"`
}

type AssetMarketsResponse struct {
	Code    uint64              `json:"code"`
	Message string              `json:"message"`
	Result  []AssetMarketV2Item `json:"result"`
}

type AssetVenuesRequest = AssetMarketsRequest
type AssetVenueV2Item = AssetMarketV2Item
type AssetVenuesResponse = AssetMarketsResponse

type ProviderCatalogAuditRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Provider      string `json:"provider"`
	Status        string `json:"status"`
	Page          int64  `json:"page"`
	PageSize      int64  `json:"page_size"`
	RankLimit     int    `json:"rank_limit"`
}

type ProviderCatalogAuditItem struct {
	Provider         string  `json:"provider"`
	SourceSymbol     string  `json:"source_symbol"`
	MarketType       string  `json:"market_type"`
	BaseAlias        string  `json:"base_alias"`
	QuoteAlias       string  `json:"quote_alias"`
	UpstreamStatus   *string `json:"upstream_status"`
	ResolutionStatus string  `json:"resolution_status"`
	BaseAssetID      *string `json:"base_asset_id"`
	QuoteAssetID     *string `json:"quote_asset_id"`
	Reason           *string `json:"reason"`
	LastSeenAt       int64   `json:"last_seen_at"`
	Rank             *int    `json:"rank"`
	CandidateKind    string  `json:"candidate_kind"`
	AliasReview      string  `json:"alias_review"`
	RolloutMode      string  `json:"rollout_mode"`
	ResolutionSource string  `json:"resolution_source"`
}

type CatalogAuditCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type ProviderCatalogAuditResponse struct {
	Code    uint64                     `json:"code"`
	Message string                     `json:"message"`
	Result  []ProviderCatalogAuditItem `json:"result"`
	Counts  []CatalogAuditCount        `json:"counts"`
	Total   int64                      `json:"total"`
}
