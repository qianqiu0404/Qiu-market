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
}

type MarketDashboardItem struct {
	Symbol           string `json:"symbol"`
	Price            string `json:"price"`
	Change24h        string `json:"change24h"`
	Volume           string `json:"volume"`
	MarketCap        string `json:"market_cap"`
	Name             string `json:"name"`
	Logo             string `json:"logo"`
	UpdatedAt        int64  `json:"updated_at"`
	DataDelaySeconds int64  `json:"data_delay_seconds"`
}

type MarketDashboardResponse struct {
	Code    uint64                `json:"code"`
	Message string                `json:"message"`
	Result  []MarketDashboardItem `json:"result"`
	Total   int64                 `json:"total"`
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
}

type SymbolResponse struct {
	Code    uint64   `json:"code"`
	Message string   `json:"message"`
	Result  []Symbol `json:"result"`
	Total   int64    `json:"total"`
}

type KlinesRequest struct {
	SymbolGuid string `json:"symbol_guid"`
	Limit      int64  `json:"limit"`
	Interval   string `json:"interval"`
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
