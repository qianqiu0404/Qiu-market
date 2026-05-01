package routes

import (
	"encoding/json"
	"net/http"

	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h Routes) GetSystemOverview(w http.ResponseWriter, r *http.Request) {
	var req model.CommonRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := h.srv.GetSystemOverview(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = jsonResponse(w, res, http.StatusOK)
}
