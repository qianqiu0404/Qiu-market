package routes

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/http/model"
)

const (
	InternalServerError = "Internal server error"
	BadRequestCode      = 4000
	InternalErrorCode   = 5000
)

func jsonResponse(w http.ResponseWriter, data interface{}, statusCode int) error {
	w.Header().Set("Content-Type", "application/json")
	jsonData, err := json.Marshal(data)
	if err != nil {
		writeFallbackJSONError(w, http.StatusInternalServerError, InternalServerError)
		return err
	}

	w.WriteHeader(statusCode)
	_, err = w.Write(jsonData)
	if err != nil {
		log.Error("write json response failed", "error", err)
		return err
	}

	return nil
}

func jsonErrorResponse(w http.ResponseWriter, code uint64, message string, statusCode int) {
	if err := jsonResponse(w, model.ErrorResponse{
		Code:    code,
		Message: message,
		Result:  nil,
	}, statusCode); err != nil {
		log.Error("write json error response failed", "error", err)
	}
}

func writeFallbackJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":%q,"result":null}`, InternalErrorCode, message)
}
