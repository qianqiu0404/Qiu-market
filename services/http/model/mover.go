package model

// TopMoversRequest 是 /api/v1/get_top_movers 的请求体。
// Direction 只允许 "gainers"（涨幅榜）或 "losers"（跌幅榜）；
// Limit 缺省 5、上限 20，由 service 层统一收敛。
type TopMoversRequest struct {
	ConsumerToken string `json:"consumer_token"`
	Direction     string `json:"direction"`
	Limit         int64  `json:"limit"`
}

// TopMoverItem 字段与 get_market_dashboard 的条目一致，额外带 rank。
// 数值字段保持字符串（价格/成交量/市值为 1e8 放大整数还原后的十进制串），
// 与 dashboard 接口保持同一序列化约定。
type TopMoverItem struct {
	Rank             int64  `json:"rank"`
	MarketID         string `json:"market_id"`
	MarketCode       string `json:"market_code"`
	Symbol           string `json:"symbol"`
	Price            string `json:"price"`
	Change24h        string `json:"change24h"`
	Volume           string `json:"volume"`
	MarketCap        string `json:"market_cap"`
	Name             string `json:"name"`
	Logo             string `json:"logo"`
	Exchange         string `json:"exchange"`
	MarketType       string `json:"market_type"`
	ChangeAvailable  bool   `json:"change_available"`
	UpdatedAt        int64  `json:"updated_at"`
	DataDelaySeconds int64  `json:"data_delay_seconds"`
}

type TopMoversResponse struct {
	Code    uint64         `json:"code"`
	Message string         `json:"message"`
	Result  []TopMoverItem `json:"result"`
	Total   int64          `json:"total"`
	// Direction 回显收敛后的方向（gainers/losers）。
	Direction string `json:"direction"`
	// Source 标明本次榜单来源："redis"（ZSET）或 "sql"（回退），便于排障。
	Source string `json:"source"`
}
