package model

type ErrorResponse struct {
	Code    uint64      `json:"code"`
	Message string      `json:"message"`
	Result  interface{} `json:"result"`
}
