package bybit

import (
	"github.com/ccxt/ccxt/go/v4"
	"github.com/ethereum/go-ethereum/log"
)

type BybitClient struct {
	BybitCli *ccxt.Bybit
}

func NewBybitClient() (*BybitClient, error) {
	binanceCli := ccxt.NewBybit(map[string]interface{}{
		"enableRateLimit": true,
	})
	return &BybitClient{
		BybitCli: binanceCli,
	}, nil
}

func (bc *BybitClient) FetchOrderBook(symbol string) (*ccxt.OrderBook, error) {
	_, err := bc.BybitCli.LoadMarkets()
	if err != nil {
		log.Error("binance load markets error:", err)
		return nil, err
	}
	book, err := bc.BybitCli.FetchOrderBook(symbol)
	if err != nil {
		log.Error("fetch binance orderbook fail", "error", err)
		return nil, err
	}
	return &book, nil
}
