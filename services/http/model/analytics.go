package model

// KlineAnalyticsRequest 是 /api/v1/get_kline_analytics 的请求体。
// Interval 只允许 1m/15m/1h/1d（缺省 1m）；Limit 缺省 20、上限 100，
// 由 service 层统一收敛，与 get_klines / get_top_movers 的收敛方式一致。
type KlineAnalyticsRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Interval      string `json:"interval"`
	Limit         int64  `json:"limit"`
}

// KlineAnalyticsItem 是单个交易对在某一周期上的聚合分析结果。
// 小数指标（涨跌幅 / 波动率 / 均价 / 成交量）以字符串序列化，与 dashboard /
// klines 接口的「数字字符串」约定一致；计算在 Doris 侧完成（列存聚合），
// 服务层不做二次计算。交易对名称由服务层按 symbol/asset 表映射填充
// （与 top movers 同一模式），查不到时回退为 guid，前端无需二次 join。
type KlineAnalyticsItem struct {
	SymbolGuid     string `json:"symbol_guid"`
	SymbolName     string `json:"symbol_name"` // 如 BTC/USDT；映射失败时等于 symbol_guid
	BaseAsset      string `json:"base_asset"`  // 如 BTC；查不到为空串
	QuoteAsset     string `json:"quote_asset"` // 如 USDT；查不到为空串
	CandleCount    int64  `json:"candle_count"`
	PriceChangePct string `json:"price_change_pct"` // 周期涨跌幅 %：末根收盘 vs 首根开盘
	PeriodHigh     string `json:"period_high"`
	PeriodLow      string `json:"period_low"`
	HighLowRange   string `json:"high_low_range"`
	VolatilityPct  string `json:"volatility_pct"` // 相邻收盘收益率 (c/c_prev-1) 的 STDDEV_POP × 100
	AvgVolume      string `json:"avg_volume"`
	TotalVolume    string `json:"total_volume"`
}

type KlineAnalyticsResponse struct {
	Code    uint64               `json:"code"`
	Message string               `json:"message"`
	Result  []KlineAnalyticsItem `json:"result"`
	Total   int64                `json:"total"`
	// Interval 回显收敛后的周期，便于前端确认服务端实际使用的过滤值。
	Interval string `json:"interval"`
}

type AssetMomentumRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Window        string `json:"window"`
}

type AssetMomentumItem struct {
	AssetID         string `json:"asset_id"`
	AssetSymbol     string `json:"asset_symbol"`
	AssetName       string `json:"asset_name"`
	MarketID        string `json:"market_id"`
	MarketCode      string `json:"market_code"`
	Exchange        string `json:"exchange"`
	ReturnPct       string `json:"return_pct"`
	VolatilityPct   string `json:"volatility_pct"`
	HighLowRangePct string `json:"high_low_range_pct"`
	CandleCount     int64  `json:"candle_count"`
	ExpectedCandles int64  `json:"expected_candles"`
	CoveragePct     string `json:"coverage_pct"`
	LowCoverage     bool   `json:"low_coverage"`
}

type AssetMomentumResponse struct {
	Code        uint64              `json:"code"`
	Message     string              `json:"message"`
	Result      []AssetMomentumItem `json:"result"`
	Total       int64               `json:"total"`
	Window      string              `json:"window"`
	Interval    string              `json:"interval"`
	WindowStart int64               `json:"window_start"`
	WindowEnd   int64               `json:"window_end"`
}
