package bybit

import (
	"time"

	"github.com/ccxt/ccxt/go/v4"
	"github.com/ethereum/go-ethereum/log"
)

type BybitClient struct {
	BybitCli *ccxt.Bybit
}

func NewBybitClient(proxy string, proxyType string) (*BybitClient, error) {
	cfg := map[string]interface{}{
		"enableRateLimit": true,
		"timeout":         60000,
		"options": map[string]interface{}{
			"defaultType": "spot",
		},
	}

	switch proxyType {
	case "http":
		cfg["httpProxy"] = proxy
	case "socks5":
		cfg["socksProxy"] = proxy
	}

	cli := ccxt.NewBybit(cfg)

	_, err := cli.LoadMarkets()
	if err != nil {
		log.Error("bybit load markets error", "proxyType", proxyType, "proxy", proxy, "error", err)
		return nil, err
	}

	return &BybitClient{
		BybitCli: cli,
	}, nil
}

func (bc *BybitClient) FetchOrderBook(symbol string) (*ccxt.OrderBook, error) {
	book, err := bc.BybitCli.FetchOrderBook(symbol)
	if err != nil {
		log.Error("fetch bybit orderbook failed", "symbol", symbol, "error", err)
		return nil, err
	}
	return &book, nil
}

func (bc *BybitClient) FetchOrderBookWithRetry(symbol string, retry int) (*ccxt.OrderBook, error) {
	var lastErr error
	for i := 0; i < retry; i++ {
		book, err := bc.FetchOrderBook(symbol)
		if err == nil {
			return book, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}
