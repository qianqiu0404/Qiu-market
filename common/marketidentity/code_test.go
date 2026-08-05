package marketidentity

import "testing"

func TestGenerateMarketCode(t *testing.T) {
	tests := []struct {
		exchange, symbol, marketType, want string
	}{
		{"binance", "BTC/USDT", "SPOT", "binance:BTC/USDT:spot"},
		{"hyperliquid", "btc/usd", "PERP", "hyperliquid:BTC/USD:perp"},
	}
	for _, tc := range tests {
		got, err := GenerateMarketCode(tc.exchange, tc.symbol, tc.marketType)
		if err != nil {
			t.Fatalf("GenerateMarketCode: %v", err)
		}
		if got != tc.want {
			t.Fatalf("GenerateMarketCode = %q, want %q", got, tc.want)
		}
	}
}

func TestGenerateMarketCodeRejectsAmbiguousInput(t *testing.T) {
	for _, tc := range [][3]string{
		{"Binance", "BTC/USDT", "spot"},
		{"binance", "BTCUSDT", "spot"},
		{"binance", "BTC/USDT", "spot market"},
	} {
		if _, err := GenerateMarketCode(tc[0], tc[1], tc[2]); err == nil {
			t.Fatalf("expected error for %#v", tc)
		}
	}
}
