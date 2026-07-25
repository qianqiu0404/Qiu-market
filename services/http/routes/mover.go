package routes

import (
	"encoding/json"
	"net/http"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h Routes) GetTopMovers(w http.ResponseWriter, r *http.Request) {
	var tmReq model.TopMoversRequest
	if err := json.NewDecoder(r.Body).Decode(&tmReq); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	log.Info("decode params success", "ConsumerToken", tmReq.ConsumerToken, "direction", tmReq.Direction, "limit", tmReq.Limit)
	moversRet, err := h.srv.GetTopMovers(&tmReq)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query top movers failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, moversRet, http.StatusOK)
}
