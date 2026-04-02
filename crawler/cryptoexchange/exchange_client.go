package cryptoexchange

import (
	"github.com/ccxt/ccxt/go/v4"
	"github.com/ethereum/go-ethereum/log"
)

type ExchangeClient struct {
	BybitClient   *ccxt.Bybit
	OxkClient     *ccxt.Okx
	BinanceClient *ccxt.Binance
}

func NewExchangeClient(proxy string, proxyType string) (*ExchangeClient, error) {
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

	bybitCli := ccxt.NewBybit(cfg)
	_, err := bybitCli.LoadMarkets()
	if err != nil {
		log.Error("bybit load markets error", "proxyType", proxyType, "proxy", proxy, "error", err)
		return nil, err
	}
	log.Info("bybit create success", "proxyType", proxyType, "proxy", proxy)

	okxCli := ccxt.NewOkx(cfg)
	_, err = okxCli.LoadMarkets()
	if err != nil {
		log.Error("okx load markets error", "proxyType", proxyType, "proxy", proxy, "error", err)
		return nil, err
	}
	log.Info("oxk create success", "proxyType", proxyType, "proxy", proxy)

	binanceCli := ccxt.NewBinance(cfg)
	_, err = binanceCli.LoadMarkets()
	if err != nil {
		log.Error("binance load markets error", "proxyType", proxyType, "proxy", proxy, "error", err)
		return nil, err
	}
	log.Info("binance create success", "proxyType", proxyType, "proxy", proxy)

	return &ExchangeClient{
		BybitClient:   bybitCli,
		OxkClient:     okxCli,
		BinanceClient: binanceCli,
	}, nil
}

func (ec *ExchangeClient) FetchOrderBook(exchangeName, symbol string) (*ccxt.OrderBook, error) {
	var orderBook ccxt.OrderBook
	var err error
	switch exchangeName {
	case "Binance":
		orderBook, err = ec.BinanceClient.FetchOrderBook(symbol)
		if err != nil {
			log.Error("binance fetch order book error", "exchangeName", exchangeName, "symbol", symbol, "error", err)
			return nil, err
		}
	case "Okx":
		orderBook, err = ec.OxkClient.FetchOrderBook(symbol)
		if err != nil {
			log.Error("binance fetch order book error", "exchangeName", exchangeName, "symbol", symbol, "error", err)
			return nil, err
		}
	case "Bybit":
		orderBook, err = ec.BybitClient.FetchOrderBook(symbol)
		if err != nil {
			log.Error("binance fetch order book error", "exchangeName", exchangeName, "symbol", symbol, "error", err)
			return nil, err
		}
	}
	return &orderBook, nil
}
