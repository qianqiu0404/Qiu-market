package routes

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/the-web3/s78-market-services/services/http/model"
	"github.com/the-web3/s78-market-services/services/http/service"
)

func (h Routes) GetKlineAnalytics(w http.ResponseWriter, r *http.Request) {
	var body model.KlineAnalyticsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetKlineAnalytics(&body)
	if err != nil {
		// 数仓未配置 / 不可达：标准错误信封 + 明确原因，前端渲染 ErrorState。
		// 绝不返回假分析数据，也不静默回退 PG。
		if errors.Is(err, service.ErrDorisUnavailable) {
			jsonErrorResponse(w, InternalErrorCode, err.Error(), http.StatusServiceUnavailable)
			return
		}
		jsonErrorResponse(w, InternalErrorCode, "query kline analytics failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetAssetMomentum(w http.ResponseWriter, r *http.Request) {
	var body model.AssetMomentumRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetAssetMomentum(&body)
	if err != nil {
		if errors.Is(err, service.ErrDorisUnavailable) {
			jsonErrorResponse(w, InternalErrorCode, err.Error(), http.StatusServiceUnavailable)
			return
		}
		jsonErrorResponse(w, InternalErrorCode, "query asset momentum failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}
