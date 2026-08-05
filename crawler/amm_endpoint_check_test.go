package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointCheckConfigIsProviderSpecific(t *testing.T) {
	uniswap, err := endpointCheckConfig("uniswap")
	require.NoError(t, err)
	require.Equal(t, int64(1), uniswap.ChainID)

	pancake, err := endpointCheckConfig("pancakeswap")
	require.NoError(t, err)
	require.Equal(t, int64(56), pancake.ChainID)

	_, err = endpointCheckConfig("binance")
	require.Error(t, err)
}

func TestCheckAMMSubgraphRequiresFreshMetadataAndAPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"data":{"_meta":{"block":{"number":12345},"hasIndexingErrors":false},"pools":[{"id":"0xpool"}]}}`,
		))
	}))
	defer server.Close()

	block, pools, err := checkAMMSubgraph(
		context.Background(), server.Client(), server.URL,
	)
	require.NoError(t, err)
	require.Equal(t, int64(12345), block)
	require.Equal(t, 1, pools)
}

func TestCheckAMMEndpointsDoesNotEchoManagedURLs(t *testing.T) {
	secretURL := "https://example.invalid/private-api-key"
	_, err := CheckAMMEndpoints(context.Background(), "uniswap", secretURL, secretURL)
	require.Error(t, err)
	require.NotContains(t, err.Error(), secretURL)
	require.NotContains(t, err.Error(), "private-api-key")
}

func TestCheckPublicAMMDiscoveryRequiresMatchingV2OrV3Pool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		require.Equal(t, http.MethodGet, request.Method)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
		  {
		    "chainId":"ethereum",
		    "dexId":"uniswap",
		    "pairAddress":"0x3416cF6C708Da44DB2624D63ea0AAef7113527C6",
		    "labels":["v3"]
		  },
		  {
		    "chainId":"ethereum",
		    "dexId":"uniswap",
		    "pairAddress":"0x0000000000000000000000000000000000000002",
		    "labels":["v2"]
		  },
		  {
		    "chainId":"ethereum",
		    "dexId":"uniswap",
		    "pairAddress":"0x0fb0e40cec3bb23e13abc585958a93c796fbea56955e19a23727a716a0423239",
		    "labels":["v4"]
		  }
		]`))
	}))
	defer server.Close()

	config := uniswapAMMConfig
	config.DiscoveryURL = server.URL
	count, err := checkPublicAMMDiscovery(
		context.Background(), server.Client(), config,
	)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}
