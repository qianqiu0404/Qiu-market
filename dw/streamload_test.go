package dw

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/config"
)

func testStreamLoader(server *httptest.Server) *StreamLoader {
	loader := NewStreamLoader(config.DorisConfig{
		Host:     strings.TrimPrefix(server.URL, "http://"),
		HttpPort: 0,
		Database: "market",
		User:     "operator",
		Password: "super-secret",
	})
	loader.httpBase = server.URL
	return loader
}

func writeStreamLoadResponse(t *testing.T, writer http.ResponseWriter, statusCode int, response streamLoadResponse) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	require.NoError(t, json.NewEncoder(writer).Encode(response))
}

func TestStreamLoaderSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPut, request.Method)
		require.Equal(t, "batch-1", request.Header.Get("label"))
		require.Equal(t, "100-continue", request.Header.Get("Expect"))
		user, password, ok := request.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "operator", user)
		require.Equal(t, "super-secret", password)
		payload, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `[{"market_id":"m1"}]`, string(payload))
		writeStreamLoadResponse(t, writer, http.StatusOK, streamLoadResponse{
			Status:           "Success",
			Label:            "batch-1",
			NumberLoadedRows: 1,
		})
	}))
	defer server.Close()

	loaded, err := testStreamLoader(server).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"batch-1",
		[]byte(`[{"market_id":"m1"}]`),
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, loaded)
}

func TestStreamLoaderRedirectPreservesProtocolHeaders(t *testing.T) {
	var backend *httptest.Server
	backend = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "redirected-batch", request.Header.Get("label"))
		require.Equal(t, "100-continue", request.Header.Get("Expect"))
		user, password, ok := request.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "operator", user)
		require.Equal(t, "super-secret", password)
		writeStreamLoadResponse(t, writer, http.StatusOK, streamLoadResponse{
			Status:           "Success",
			NumberLoadedRows: 2,
		})
	}))
	defer backend.Close()

	frontend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", backend.URL+"/api/market/dwd_market_kline_v2/_stream_load")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer frontend.Close()

	loaded, err := testStreamLoader(frontend).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"redirected-batch",
		[]byte(`[{},{}]`),
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, loaded)
}

func TestStreamLoaderHTTPErrorIsBoundedAndCredentialSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeStreamLoadResponse(t, writer, http.StatusBadRequest, streamLoadResponse{
			Status:  "Fail",
			Label:   "bad-batch",
			Message: "column mismatch\ninvalid sync_seq",
		})
	}))
	defer server.Close()

	_, err := testStreamLoader(server).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"bad-batch",
		[]byte(`[{"password":"payload-secret"}]`),
	)
	require.Error(t, err)
	var loadErr *StreamLoadError
	require.True(t, errors.As(err, &loadErr))
	require.Equal(t, http.StatusBadRequest, loadErr.HTTPStatus)
	require.Equal(t, "Fail", loadErr.Status)
	require.Equal(t, "bad-batch", loadErr.Label)
	require.Contains(t, loadErr.Summary, "column mismatch invalid sync_seq")
	require.NotContains(t, err.Error(), "super-secret")
	require.NotContains(t, err.Error(), "payload-secret")
}

func TestStreamLoaderFinishedLabelIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeStreamLoadResponse(t, writer, http.StatusOK, streamLoadResponse{
			Status:            "Label Already Exists",
			Label:             "same-batch",
			ExistingJobStatus: "FINISHED",
		})
	}))
	defer server.Close()

	loaded, err := testStreamLoader(server).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"same-batch",
		[]byte(`[]`),
	)
	require.NoError(t, err)
	require.Zero(t, loaded)
}

func TestStreamLoaderRetriesBareHTMLBadRequestWithSameLabel(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "retry-batch", request.Header.Get("label"))
		if attempts.Add(1) == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte("<html><body>Bad Request</body></html>"))
			return
		}
		writeStreamLoadResponse(t, writer, http.StatusOK, streamLoadResponse{
			Status:           "Success",
			Label:            "retry-batch",
			NumberLoadedRows: 1,
		})
	}))
	defer server.Close()

	loaded, err := testStreamLoader(server).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"retry-batch",
		[]byte(`[{"market_id":"m1"}]`),
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, loaded)
	require.EqualValues(t, 2, attempts.Load())
}

func TestStreamLoaderDoesNotRetryParsedBadRequest(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writeStreamLoadResponse(t, writer, http.StatusBadRequest, streamLoadResponse{
			Status:  "Fail",
			Label:   "schema-error",
			Message: "column mismatch",
		})
	}))
	defer server.Close()

	_, err := testStreamLoader(server).Load(
		context.Background(),
		"dwd_market_kline_v2",
		"schema-error",
		[]byte(`[{"market_id":"m1"}]`),
	)
	require.Error(t, err)
	require.EqualValues(t, 1, attempts.Load())
}
