package routes

import (
	"encoding/json"
	"net/http"

	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h Routes) GetKlines(w http.ResponseWriter, r *http.Request) {
	var body model.KlinesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErrorResponse(w, BadRequestCode, "invalid JSON body", http.StatusBadRequest)
		return
	}
	resp, err := h.srv.GetKlines(&body)
	if err != nil {
		jsonErrorResponse(w, InternalErrorCode, "query klines failed", http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, resp, http.StatusOK)
}
