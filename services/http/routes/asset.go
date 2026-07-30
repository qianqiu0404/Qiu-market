package routes

import (
	"encoding/json"
	"errors"

	"net/http"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h Routes) GetSupportAssets(w http.ResponseWriter, r *http.Request) {
	var saReq model.SupportAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&saReq); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	log.Info("decode params success", "ConsumerToken", saReq.ConsumerToken)
	supRet, err := h.srv.GetSupportAssets(&saReq)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query support assets failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, supRet, http.StatusOK)
}

func (h Routes) GetMarketDashboard(w http.ResponseWriter, r *http.Request) {
	var mdReq model.MarketDashboardRequest
	if err := json.NewDecoder(r.Body).Decode(&mdReq); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	log.Info("decode params success", "ConsumerToken", mdReq.ConsumerToken)
	dashRet, err := h.srv.GetMarketDashboard(&mdReq)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query market dashboard failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, dashRet, http.StatusOK)
}

func (h Routes) GetAssetDashboard(w http.ResponseWriter, r *http.Request) {
	var req model.AssetDashboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetAssetDashboard(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query asset dashboard failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetMarketInsights(w http.ResponseWriter, r *http.Request) {
	var req model.MarketInsightsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetMarketInsights(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query market insights failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetMarketOverview(w http.ResponseWriter, r *http.Request) {
	var req model.MarketOverviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetMarketOverview(&req)
	if err != nil {
		if errors.Is(err, database.ErrInvalidDashboardVenue) {
			jsonErrorResponse(w, BadRequestCode, err.Error(), http.StatusBadRequest)
			return
		}
		jsonErrorResponse(w, InternalErrorCode, "query market overview failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetAssetDashboardV2(w http.ResponseWriter, r *http.Request) {
	var req model.AssetDashboardV2Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetAssetDashboardV2(&req)
	if err != nil {
		if errors.Is(err, database.ErrInvalidDashboardVenue) {
			jsonErrorResponse(w, BadRequestCode, err.Error(), http.StatusBadRequest)
			return
		}
		jsonErrorResponse(w, InternalErrorCode, "query asset dashboard v2 failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetMarketPriceTicks(w http.ResponseWriter, r *http.Request) {
	var req model.MarketPriceTicksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(req.AssetIDs) > 100 {
		jsonErrorResponse(w, BadRequestCode, "asset_ids cannot contain more than 100 items", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetMarketPriceTicks(&req)
	if err != nil {
		if errors.Is(err, database.ErrInvalidDashboardVenue) {
			jsonErrorResponse(w, BadRequestCode, err.Error(), http.StatusBadRequest)
			return
		}
		jsonErrorResponse(w, InternalErrorCode, "query market price ticks failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetAssetMarkets(w http.ResponseWriter, r *http.Request) {
	var req model.AssetMarketsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.AssetID == "" {
		jsonErrorResponse(w, BadRequestCode, "asset_id is required", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetAssetMarkets(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query asset markets failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetAssetVenues(w http.ResponseWriter, r *http.Request) {
	var req model.AssetVenuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.AssetID == "" {
		jsonErrorResponse(w, BadRequestCode, "asset_id is required", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetAssetVenues(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query asset venues failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetProviderCatalogAudit(w http.ResponseWriter, r *http.Request) {
	var req model.ProviderCatalogAuditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetProviderCatalogAudit(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query provider catalog audit failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}

func (h Routes) GetExchanges(w http.ResponseWriter, r *http.Request) {
	var req model.CommonRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetExchanges(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query exchanges failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}

func (h Routes) GetSymbols(w http.ResponseWriter, r *http.Request) {
	var req model.CommonRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetSymbols(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query symbols failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}

func (h Routes) GetFiatRates(w http.ResponseWriter, r *http.Request) {
	var req model.FiatRatesRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetFiatRates(&req)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query fiat rates failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}
