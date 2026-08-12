package rest

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarketJSONCompressionKeepsTheFullPayload(t *testing.T) {
	payload := `{"code":2000,"result":"` + strings.Repeat("market-price-fact-", 4_096) + `"}`
	handler := marketJSONCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, err := io.WriteString(w, payload)
		require.NoError(t, err)
	}))

	request := httptest.NewRequest(http.MethodPost, AssetDashboardV2Path, nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	require.Less(t, response.Body.Len(), len(payload)/10)
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, payload, string(decoded))
}
