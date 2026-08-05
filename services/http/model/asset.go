package model

type CommonRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Page          int64  `json:"page"`
	PageSize      int64  `json:"page_size"`
}

type SupportAssetRequest struct {
	ConsumerToken string `json:"consumer_token"`
}

type SupportAsset struct {
	Guid        string `json:"guid"`
	AssetName   string `json:"asset_name"`
	AssetSymbol string `json:"asset_symbol"`
	AssetLogo   string `json:"asset_logo"`
}

type SupportAssetResponse struct {
	Code    uint64         `json:"code"`
	Message string         `json:"message"`
	Result  []SupportAsset `json:"result"`
}

type MarketDashboardRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Page          int64  `json:"page"`
	PageSize      int64  `json:"page_size"`
	// Exchange 可选：按交易所名过滤（如 "Binance" / "Hyperliquid"），空串表示不过滤。
	Exchange      string `json:"exchange"`
	Search        string `json:"search"`
	MarketID      string `json:"market_id"`
	SortBy        string `json:"sort_by"`
	SortDirection string `json:"sort_direction"`
}

type MarketDashboardItem struct {
	MarketID          string `json:"market_id"`
	MarketCode        string `json:"market_code"`
	Symbol            string `json:"symbol"`
	Price             string `json:"price"`
	Change24h         string `json:"change24h"`
	Volume            string `json:"volume"`
	MarketCap         string `json:"market_cap"`
	Name              string `json:"name"`
	Logo              string `json:"logo"`
	Exchange          string `json:"exchange"`
	MarketType        string `json:"market_type"`
	HasKline          bool   `json:"has_kline"`
	ChangeAvailable   bool   `json:"change_available"`
	ChangeSource      string `json:"change_source"`
	UpdatedAt         int64  `json:"updated_at"`
	DataDelaySeconds  int64  `json:"data_delay_seconds"`
	BaseAssetID       string `json:"base_asset_id"`
	BaseAsset         string `json:"base_asset"`
	QuoteAssetID      string `json:"quote_asset_id"`
	QuoteAsset        string `json:"quote_asset"`
	ProviderUpdatedAt int64  `json:"provider_updated_at"`
	FreshnessStatus   string `json:"freshness_status"`
}

type MarketDashboardResponse struct {
	Code    uint64                `json:"code"`
	Message string                `json:"message"`
	Result  []MarketDashboardItem `json:"result"`
	Total   int64                 `json:"total"`
}

type AssetDashboardRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Page          int64  `json:"page"`
	PageSize      int64  `json:"page_size"`
	Search        string `json:"search"`
	SortBy        string `json:"sort_by"`
	SortDirection string `json:"sort_direction"`
}

type AssetMarketItem struct {
	MarketID          string `json:"market_id"`
	MarketCode        string `json:"market_code"`
	Symbol            string `json:"symbol"`
	Exchange          string `json:"exchange"`
	MarketType        string `json:"market_type"`
	QuoteAssetID      string `json:"quote_asset_id"`
	QuoteAsset        string `json:"quote_asset"`
	Price             string `json:"price"`
	Change24h         string `json:"change24h"`
	ChangeAvailable   bool   `json:"change_available"`
	Volume            string `json:"volume"`
	MarketCap         string `json:"market_cap"`
	HasKline          bool   `json:"has_kline"`
	UpdatedAt         int64  `json:"updated_at"`
	DataDelaySeconds  int64  `json:"data_delay_seconds"`
	IsReference       bool   `json:"is_reference"`
	ProviderUpdatedAt int64  `json:"provider_updated_at"`
	FreshnessStatus   string `json:"freshness_status"`
}

type AssetDashboardItem struct {
	AssetID             string            `json:"asset_id"`
	AssetSymbol         string            `json:"asset_symbol"`
	AssetName           string            `json:"asset_name"`
	Logo                string            `json:"logo"`
	ReferenceMarketID   string            `json:"reference_market_id"`
	ReferenceMarketCode string            `json:"reference_market_code"`
	ReferenceExchange   string            `json:"reference_exchange"`
	ReferenceMarketType string            `json:"reference_market_type"`
	Price               string            `json:"price"`
	Change24h           string            `json:"change24h"`
	ChangeAvailable     bool              `json:"change_available"`
	MarketCap           string            `json:"market_cap"`
	Turnover24h         string            `json:"turnover24h"`
	MarketCount         int64             `json:"market_count"`
	HasKline            bool              `json:"has_kline"`
	UpdatedAt           int64             `json:"updated_at"`
	DataDelaySeconds    int64             `json:"data_delay_seconds"`
	Markets             []AssetMarketItem `json:"markets"`
	ProviderUpdatedAt   int64             `json:"provider_updated_at"`
	FreshnessStatus     string            `json:"freshness_status"`
}

type AssetDashboardResponse struct {
	Code    uint64               `json:"code"`
	Message string               `json:"message"`
	Result  []AssetDashboardItem `json:"result"`
	Total   int64                `json:"total"`
}

type MarketInsightsRequest struct {
	ConsumerToken string `json:"consumer_token"`
}

type MarketBreadth struct {
	AssetCount      int64  `json:"asset_count"`
	Advancers       int64  `json:"advancers"`
	Decliners       int64  `json:"decliners"`
	Flat            int64  `json:"flat"`
	Unknown         int64  `json:"unknown"`
	AdvanceRatio    string `json:"advance_ratio"`
	MedianChange24h string `json:"median_change24h"`
	Turnover24h     string `json:"turnover24h"`
}

type ChangeDistributionBucket struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Min   string `json:"min"`
	Max   string `json:"max"`
	Count int64  `json:"count"`
}

type CrossVenueItem struct {
	AssetID             string `json:"asset_id"`
	AssetSymbol         string `json:"asset_symbol"`
	AssetName           string `json:"asset_name"`
	SpotMarketID        string `json:"spot_market_id"`
	SpotMarketCode      string `json:"spot_market_code"`
	SpotExchange        string `json:"spot_exchange"`
	SpotQuoteAsset      string `json:"spot_quote_asset"`
	SpotPrice           string `json:"spot_price"`
	SpotChange24h       string `json:"spot_change24h"`
	SpotChangeAvailable bool   `json:"spot_change_available"`
	SpotTurnover24h     string `json:"spot_turnover24h"`
	SpotTurnoverShare   string `json:"spot_turnover_share"`
	SpotDelaySeconds    int64  `json:"spot_delay_seconds"`
	PerpMarketID        string `json:"perp_market_id"`
	PerpMarketCode      string `json:"perp_market_code"`
	PerpExchange        string `json:"perp_exchange"`
	PerpQuoteAsset      string `json:"perp_quote_asset"`
	PerpPrice           string `json:"perp_price"`
	PerpChange24h       string `json:"perp_change24h"`
	PerpChangeAvailable bool   `json:"perp_change_available"`
	PerpTurnover24h     string `json:"perp_turnover24h"`
	PerpTurnoverShare   string `json:"perp_turnover_share"`
	PerpDelaySeconds    int64  `json:"perp_delay_seconds"`
	IndicativeSpreadPct string `json:"indicative_spread_pct"`
	SpreadAvailable     bool   `json:"spread_available"`
	ChangeGapPctPoints  string `json:"change_gap_pct_points"`
	ChangeGapAvailable  bool   `json:"change_gap_available"`
}

type MarketInsightsResult struct {
	Breadth      MarketBreadth              `json:"breadth"`
	Distribution []ChangeDistributionBucket `json:"distribution"`
	CrossVenue   []CrossVenueItem           `json:"cross_venue"`
	UpdatedAt    int64                      `json:"updated_at"`
}

type MarketInsightsResponse struct {
	Code    uint64               `json:"code"`
	Message string               `json:"message"`
	Result  MarketInsightsResult `json:"result"`
}

type Exchange struct {
	Guid string `json:"guid"`
	Name string `json:"name"`
	Logo string `json:"logo"`
}

type ExchangeResponse struct {
	Code    uint64     `json:"code"`
	Message string     `json:"message"`
	Result  []Exchange `json:"result"`
	Total   int64      `json:"total"`
}

type Symbol struct {
	Guid         string `json:"guid"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	SymbolName   string `json:"symbol_name"`
	BaseAssetId  string `json:"base_asset_id"`
	QuoteAssetId string `json:"quote_asset_id"`
	Exchange     string `json:"exchange"`
	MarketType   string `json:"market_type"`
}

type SymbolResponse struct {
	Code    uint64   `json:"code"`
	Message string   `json:"message"`
	Result  []Symbol `json:"result"`
	Total   int64    `json:"total"`
}

type KlinesRequest struct {
	MarketID   string `json:"market_id"`
	SymbolGuid string `json:"symbol_guid"`
	Limit      int64  `json:"limit"`
	Interval   string `json:"interval"`
}

type MarketSparklinesRequest struct {
	MarketIDs []string `json:"market_ids"`
	Interval  string   `json:"interval"`
	Limit     int64    `json:"limit"`
}

type SparklinePoint struct {
	Timestamp int64  `json:"timestamp"`
	Close     string `json:"close"`
}

type MarketSparkline struct {
	MarketID string           `json:"market_id"`
	Points   []SparklinePoint `json:"points"`
}

type MarketSparklinesResponse struct {
	Code    uint64            `json:"code"`
	Message string            `json:"message"`
	Result  []MarketSparkline `json:"result"`
}

type KlineItem struct {
	Timestamp int64  `json:"timestamp"`
	Open      string `json:"open"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Close     string `json:"close"`
	Volume    string `json:"volume"`
}

type KlinesResponse struct {
	Code    uint64      `json:"code"`
	Message string      `json:"message"`
	Result  []KlineItem `json:"result"`
}
