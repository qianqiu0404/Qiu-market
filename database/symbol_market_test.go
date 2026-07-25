package database

import (
	"strings"
	"testing"
)

func TestSymbolMarketOrderDefaultsToCapThenVolume(t *testing.T) {
	order := symbolMarketOrder("", "", nil)
	for _, fragment := range []string{
		"CASE WHEN symbol_market.market_cap > 0 THEN 0 ELSE 1 END ASC",
		"symbol_market.market_cap DESC",
		"symbol_market.volume DESC",
		"symbol_market.market_id ASC",
	} {
		if !strings.Contains(order, fragment) {
			t.Fatalf("default order %q does not contain %q", order, fragment)
		}
	}
}

func TestSymbolMarketChangeOrderUsesRedisRank(t *testing.T) {
	order := symbolMarketOrder("change24h", "desc", []string{"s2", "s1"})
	if order != "change_rank.rank ASC NULLS LAST, symbol_market.market_id ASC" {
		t.Fatalf("rank order = %q", order)
	}
}
