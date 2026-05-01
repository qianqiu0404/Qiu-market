package model

type SystemOverviewResponse struct {
	Code    uint64         `json:"code"`
	Message string         `json:"message"`
	Result  SystemOverview `json:"result"`
}

type SystemOverview struct {
	CrawlerStatus  string `json:"crawler_status"`
	RedisStatus    string `json:"redis_status"`
	DatabaseStatus string `json:"database_status"`
	WorkerStatus   string `json:"worker_status"`
	ApiStatus      string `json:"api_status"`
	MarketCount    int64  `json:"market_count"`
	AssetCount     int64  `json:"asset_count"`
	SymbolCount    int64  `json:"symbol_count"`
	ExchangeCount  int64  `json:"exchange_count"`
	TotalMarketCap string `json:"total_market_cap"`
	TotalVolume    string `json:"total_volume"`
	UpdatedAt      int64  `json:"updated_at"`
}
