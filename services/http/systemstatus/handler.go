package systemstatus

import (
	"encoding/json"
	"net/http"
)

const Path = "/api/v1/get_system_status"

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code":    4000,
			"message": "method not allowed",
		})
		return
	}
	if h == nil || h.service == nil {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code":    5000,
			"message": "system status service unavailable",
		})
		return
	}
	response := Response{
		Code: 2000, Message: "success",
		Result: h.service.Snapshot(request.Context()),
	}
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response)
}
