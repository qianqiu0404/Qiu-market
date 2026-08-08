package worker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarketPriceHandleStopsCleanly(t *testing.T) {
	handle, err := NewMarketPriceHandle(nil, nil, func(error) {})
	require.NoError(t, err)

	require.NoError(t, handle.Start())
	require.NoError(t, handle.Close())
}
