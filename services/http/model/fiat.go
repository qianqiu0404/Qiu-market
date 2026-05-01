package model

type FiatRatesRequest struct {
	ConsumerToken string `json:"consumer_token"`
}

type FiatRatesResult struct {
	Base   string            `json:"base"`
	Rates  map[string]float64 `json:"rates"`
	Source string            `json:"source"`
}

type FiatRatesResponse struct {
	Code    uint64          `json:"code"`
	Message string          `json:"message"`
	Result  FiatRatesResult `json:"result"`
}
