package service

import (
	"context"
	"strings"
	"testing"

	"github.com/the-web3/s78-market-services/trading/marketmaker"
)

type fixedReferenceSource struct{}

func (fixedReferenceSource) Current(context.Context) (marketmaker.Reference, error) {
	return marketmaker.Reference{Price: 1}, nil
}

func TestDiskAwareReferenceSourceFailsClosed(t *testing.T) {
	t.Parallel()
	source := diskAwareReferenceSource{
		next:    fixedReferenceSource{},
		path:    "/path-that-does-not-exist-qiu-market",
		minimum: 1,
	}
	_, err := source.Current(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk capacity") {
		t.Fatalf("Current error = %v", err)
	}
}
