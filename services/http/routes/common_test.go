package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func TestJSONErrorResponse(t *testing.T) {
	rec := httptest.NewRecorder()

	jsonErrorResponse(rec, InternalErrorCode, "query market dashboard failed", http.StatusInternalServerError)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp model.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint64(InternalErrorCode), resp.Code)
	require.Equal(t, "query market dashboard failed", resp.Message)
	require.Nil(t, resp.Result)
}
