package model

type SystemOverviewResponse struct {
	Code    uint64         `json:"code"`
	Message string         `json:"message"`
	Result  SystemOverview `json:"result"`
}

type SystemOverview struct {
	CrawlerStatus    string               `json:"crawler_status"`
	DexStatus        string               `json:"dex_status"`
	DwStatus         string               `json:"dw_status"`
	RpcStatus        string               `json:"rpc_status"`
	RedisStatus      string               `json:"redis_status"`
	DatabaseStatus   string               `json:"database_status"`
	WorkerStatus     string               `json:"worker_status"`
	ApiStatus        string               `json:"api_status"`
	MarketCount      int64                `json:"market_count"`
	AssetCount       int64                `json:"asset_count"`
	SymbolCount      int64                `json:"symbol_count"`
	ExchangeCount    int64                `json:"exchange_count"`
	TotalMarketCap   string               `json:"total_market_cap"`
	TotalVolume      string               `json:"total_volume"`
	UpdatedAt        int64                `json:"updated_at"`
	DataDelaySeconds int64                `json:"data_delay_seconds"`
	ProviderStatuses []ProviderStatusItem `json:"provider_statuses"`
}

type ProviderStatusItem struct {
	Provider                string                     `json:"provider"`
	Status                  string                     `json:"status"`
	OperationalStatus       string                     `json:"operational_status"`
	PrimarySourceKey        string                     `json:"primary_source_key"`
	SourceCount             int64                      `json:"source_count"`
	FailingSourceCount      int64                      `json:"failing_source_count"`
	LastAttemptAt           int64                      `json:"last_attempt_at"`
	LastSuccessAt           int64                      `json:"last_success_at"`
	LastSourceTime          int64                      `json:"last_source_time"`
	ConsecutiveFailures     int64                      `json:"consecutive_failures"`
	LastErrorClass          string                     `json:"last_error_class"`
	RolloutMode             string                     `json:"rollout_mode"`
	RankLimit               int                        `json:"rank_limit"`
	MinSoakUntil            int64                      `json:"min_soak_until"`
	NextRetryAt             int64                      `json:"next_retry_at"`
	AttemptCount            int64                      `json:"attempt_count"`
	SuccessCount            int64                      `json:"success_count"`
	SuccessRatePct          string                     `json:"success_rate_pct"`
	ObservationStartedAt    int64                      `json:"observation_started_at"`
	ReadinessNotBefore      int64                      `json:"readiness_not_before"`
	RolloutReady            bool                       `json:"rollout_ready"`
	RolloutBlockers         []string                   `json:"rollout_blockers"`
	ReceivedCount           int64                      `json:"received_count"`
	MatchedAssetCount       int64                      `json:"matched_asset_count"`
	PriceAvailableCount     int64                      `json:"price_available_count"`
	ChangeAvailableCount    int64                      `json:"change_available_count"`
	LocalPreviewEnabled     bool                       `json:"local_preview_enabled"`
	PreviewSourceKey        string                     `json:"preview_source_key"`
	PreviewCoveredCount     int64                      `json:"preview_covered_count"`
	SelectionVersion        int64                      `json:"selection_version"`
	SelectionTargetCount    int                        `json:"selection_target_count"`
	SelectionCount          int                        `json:"selection_count"`
	SelectionCandidateCount int                        `json:"selection_candidate_count"`
	SelectionGeneratedAt    int64                      `json:"selection_generated_at"`
	FeedMode                string                     `json:"feed_mode"`
	KlineStatus             string                     `json:"kline_status"`
	KlineMarketCount        int64                      `json:"kline_market_count"`
	KlineCandleCount        int64                      `json:"kline_candle_count"`
	KlineLastSuccessAt      int64                      `json:"kline_last_success_at"`
	Sources                 []ProviderSourceStatusItem `json:"sources"`
}

type ProviderSourceStatusItem struct {
	SourceKey           string `json:"source_key"`
	Capability          string `json:"capability"`
	Status              string `json:"status"`
	LastAttemptAt       int64  `json:"last_attempt_at"`
	LastSuccessAt       int64  `json:"last_success_at"`
	LastSourceTime      int64  `json:"last_source_time"`
	NextRetryAt         int64  `json:"next_retry_at"`
	ConsecutiveFailures int64  `json:"consecutive_failures"`
	AttemptCount        int64  `json:"attempt_count"`
	SuccessCount        int64  `json:"success_count"`
	SuccessRatePct      string `json:"success_rate_pct"`
	LastErrorClass      string `json:"last_error_class"`
	ReceivedCount       int64  `json:"received_count"`
	MatchedAssetCount   int64  `json:"matched_asset_count"`
	WrittenCount        int64  `json:"written_count"`
}
