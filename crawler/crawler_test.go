package crawler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCrawlerStopAllowsNilOptionalComponents(t *testing.T) {
	cl := &Crawler{}

	require.NoError(t, cl.Stop(context.Background()))
	require.True(t, cl.Stopped())
	require.NoError(t, cl.Stop(context.Background()))
}
