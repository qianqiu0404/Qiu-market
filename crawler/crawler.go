package crawler

import (
	"context"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/crawler/binance"
	"github.com/the-web3/s78-market-services/crawler/bybit"
	"github.com/the-web3/s78-market-services/crawler/fiatcurrency"
	"github.com/the-web3/s78-market-services/crawler/okx"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Crawler struct {
	BinanceCrawler      *binance.BinanceCrawler
	OkxCrawler          *okx.OkxCrawler
	BybitCrawler        *bybit.BybitCrawler
	FiatCurrencyCrawler *fiatcurrency.FiatCurrencyCrawler

	stopped atomic.Bool
}

func NewCrawler(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Crawler, error) {
	binanceOrderBook, err := binance.NewBinanceCrawler(db, redisClient, shutdown)
	if err != nil {
		log.Error("Crawler NewBinanceCrawler error", err)
		return nil, err
	}

	okxOrderBook, err := okx.NewOkxCrawler(db, redisClient, shutdown)
	if err != nil {
		log.Error("Crawler okxOrderBook error", err)
		return nil, err
	}

	bybitOrderBook, err := bybit.NewBybitCrawler(db, redisClient, shutdown)
	if err != nil {
		log.Error("Crawler okxOrderBook error", err)
		return nil, err
	}

	fiatCurrencyCrawler, err := fiatcurrency.NewFiatCurrencyCrawler(db, config, shutdown)
	if err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return nil, err
	}

	return &Crawler{
		BinanceCrawler:      binanceOrderBook,
		OkxCrawler:          okxOrderBook,
		BybitCrawler:        bybitOrderBook,
		FiatCurrencyCrawler: fiatCurrencyCrawler,
	}, nil
}

func (cl *Crawler) Start(ctx context.Context) error {
	err := cl.BinanceCrawler.Start()
	if err != nil {
		log.Error("Crawler BinanceCrawler error", err)
		return err
	}
	err = cl.OkxCrawler.Start()
	if err != nil {
		log.Error("Crawler OkxCrawler error", err)
		return err
	}
	err = cl.BybitCrawler.Start()
	if err != nil {
		log.Error("Crawler BybitCrawler error", err)
		return err
	}
	err = cl.FiatCurrencyCrawler.Start()
	if err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return err
	}
	return nil
}

func (cl *Crawler) Stop(ctx context.Context) error {
	if err := cl.BinanceCrawler.Close(); err != nil {
		log.Error("Crawler BinanceCrawler error", err)
		return err
	}

	if err := cl.OkxCrawler.Close(); err != nil {
		log.Error("Crawler OkxCrawler error", err)
		return err
	}

	if err := cl.BybitCrawler.Close(); err != nil {
		log.Error("Crawler BybitCrawler error", err)
		return err
	}

	if err := cl.FiatCurrencyCrawler.Close(); err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return err
	}
	return nil
}

func (cl *Crawler) Stopped() bool {
	return cl.stopped.Load()
}
