package main

import (
	"testing"

	"github.com/the-web3/s78-market-services/trading/netutil"
)

func TestStandaloneConfigUsesSharedLoopbackBoundary(t *testing.T) {
	t.Parallel()
	if !netutil.IsIPLoopbackAddress("127.0.0.1:9094") {
		t.Fatal("shared loopback boundary rejected standalone address")
	}
}
