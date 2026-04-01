package binance

import (
	"github.com/ccxt/ccxt/go/v4"
	"github.com/ethereum/go-ethereum/log"
)

type BinanceClient struct {
	BinanceCli *ccxt.Binance
}

func NewBinanceBinanceClient() (*BinanceClient, error) {
	binanceCli := ccxt.NewBinance(map[string]interface{}{
		"enableRateLimit": true,
	})
	return &BinanceClient{
		BinanceCli: binanceCli,
	}, nil
}

func (bc *BinanceClient) FetchOrderBook(symbol string) (*ccxt.OrderBook, error) {
	_, err := bc.BinanceCli.LoadMarkets()
	if err != nil {
		log.Error("binance load markets error:", err)
		return nil, err
	}
	book, err := bc.BinanceCli.FetchOrderBook(symbol)
	if err != nil {
		log.Error("fetch binance orderbook fail", "error", err)
		return nil, err
	}
	return &book, nil
}
