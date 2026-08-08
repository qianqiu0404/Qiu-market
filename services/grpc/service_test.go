package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarketRPCServiceStartAndStop(t *testing.T) {
	service, err := NewMarketRpcService(&MarketRpcConfig{
		Host: "127.0.0.1",
		Port: 0,
	}, nil)
	require.NoError(t, err)

	require.NoError(t, service.Start(context.Background()))
	require.NotNil(t, service.server)
	require.NotNil(t, service.listener)

	require.NoError(t, service.Stop(context.Background()))
	require.True(t, service.Stopped())
	require.NoError(t, service.Stop(context.Background()))
}
