package routes

import (
	"encoding/json"
	"fmt"

	"net/http"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h Routes) GetSupportAssets(w http.ResponseWriter, r *http.Request) {
	var saReq model.SupportAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&saReq); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	log.Info("decode params success", "ConsumerToken", saReq.ConsumerToken)
	supRet, err := h.srv.GetSupportAssets(&saReq)
	if err != nil {
		return
	}
	err = jsonResponse(w, supRet, http.StatusOK)
	if err != nil {
		fmt.Println("Error writing response", "err", err.Error())
	}
}

func (h Routes) GetMarketDashboard(w http.ResponseWriter, r *http.Request) {
	var mdReq model.MarketDashboardRequest
	if err := json.NewDecoder(r.Body).Decode(&mdReq); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	log.Info("decode params success", "ConsumerToken", mdReq.ConsumerToken)
	dashRet, err := h.srv.GetMarketDashboard(&mdReq)
	if err != nil {
		return
	}
	err = jsonResponse(w, dashRet, http.StatusOK)
	if err != nil {
		fmt.Println("Error writing response", "err", err.Error())
	}
}

func (h Routes) GetExchanges(w http.ResponseWriter, r *http.Request) {
	var req model.CommonRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetExchanges(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}

func (h Routes) GetSymbols(w http.ResponseWriter, r *http.Request) {
	var req model.CommonRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetSymbols(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}

func (h Routes) GetFiatRates(w http.ResponseWriter, r *http.Request) {
	var req model.FiatRatesRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetFiatRates(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}
