package grpc_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	grpcclient "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	marketproto "github.com/the-web3/s78-market-services/services/grpc/proto"
)

// TestLiveAssetDashboardV2 keeps the real RPC acceptance check reproducible
// without making the default unit suite depend on a running local stack.
func TestLiveAssetDashboardV2(t *testing.T) {
	address := os.Getenv("S78_TEST_GRPC_ADDR")
	if address == "" {
		t.Skip("S78_TEST_GRPC_ADDR is not set")
	}
	connection, err := grpcclient.NewClient(
		address,
		grpcclient.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	includeUncovered := true
	response, err := marketproto.NewMarketServiceClient(connection).
		GetAssetDashboardV2(ctx, &marketproto.AssetDashboardV2Request{
			Page:             1,
			PageSize:         100,
			Filter:           "assets",
			SortBy:           "rank",
			SortDirection:    "desc",
			Venue:            "all",
			IncludeUncovered: &includeUncovered,
			Universe:         "provider_union",
		})
	require.NoError(t, err)
	require.EqualValues(t, 2000, response.GetReturnCode())
	require.Equal(t, "provider_union", response.GetUniverse())
	require.NotEmpty(t, response.GetResult())
	require.EqualValues(t, len(response.GetResult()), response.GetTotal())

	seen := make(map[string]struct{}, len(response.GetResult()))
	for _, item := range response.GetResult() {
		require.NotEmpty(t, item.GetAssetId())
		_, duplicated := seen[item.GetAssetId()]
		require.False(t, duplicated, "duplicate canonical asset %s", item.GetAssetId())
		seen[item.GetAssetId()] = struct{}{}
	}
}
