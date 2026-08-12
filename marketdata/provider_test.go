package marketdata

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyProviderError(t *testing.T) {
	require.Equal(t, "restricted", ClassifyProviderError(nil, http.StatusForbidden))
	require.Equal(t, "restricted", ClassifyProviderError(nil, http.StatusUnavailableForLegalReasons))
	require.Equal(t, "rate_limited", ClassifyProviderError(nil, http.StatusTooManyRequests))
	require.Equal(t, "upstream_4xx", ClassifyProviderError(nil, http.StatusBadRequest))
	require.Equal(t, "upstream_5xx", ClassifyProviderError(nil, http.StatusBadGateway))
	require.Equal(t, "timeout", ClassifyProviderError(context.DeadlineExceeded, 0))
	require.Equal(t, "invalid_response", ClassifyProviderError(errors.New("decode payload"), 0))
	require.Equal(t, "request_error", ClassifyProviderError(errors.New("bad request"), 0))
}
