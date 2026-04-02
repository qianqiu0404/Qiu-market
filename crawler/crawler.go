package crawler

import (
	"context"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/crawler/cryptoexchange"
	"github.com/the-web3/s78-market-services/crawler/fiatcurrency"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Crawler struct {
	ExchangeOrderbook   *cryptoexchange.ExchangeOrderbook
	FiatCurrencyCrawler *fiatcurrency.FiatCurrencyCrawler
	stopped             atomic.Bool
}

func NewCrawler(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Crawler, error) {
	exchangeOrderbook, err := cryptoexchange.NewExchangeOrderbook(db, redisClient, shutdown)
	if err != nil {
		log.Error("Crawler NewBinanceCrawler error", err)
		return nil, err
	}

	fiatCurrencyCrawler, err := fiatcurrency.NewFiatCurrencyCrawler(db, config, shutdown)
	if err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return nil, err
	}

	return &Crawler{
		ExchangeOrderbook:   exchangeOrderbook,
		FiatCurrencyCrawler: fiatCurrencyCrawler,
	}, nil
}

func (cl *Crawler) Start(ctx context.Context) error {
	err := cl.ExchangeOrderbook.Start()
	if err != nil {
		log.Error("Crawler ExchangeOrderbook Start error", err)
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
	if err := cl.ExchangeOrderbook.Close(); err != nil {
		log.Error("Crawler ExchangeOrderbook Stop error", err)
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
