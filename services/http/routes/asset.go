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
	supRet, err := h.srv.GetSupportAsset(&saReq)
	if err != nil {
		return
	}
	err = jsonResponse(w, supRet, http.StatusOK)
	if err != nil {
		fmt.Println("Error writing response", "err", err.Error())
	}
}
