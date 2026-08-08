package crawler

import (
	"context"
	"errors"
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
	BinanceTicker       *BinanceTickerCrawler
	db                  *database.DB
	redisClient         *redis.Client
	stopped             atomic.Bool
}

func NewCrawler(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Crawler, error) {
	/*
		exchangeOrderbook, err := cryptoexchange.NewExchangeOrderbook(db, redisClient, shutdown)
		if err != nil {
			log.Error("Crawler NewBinanceCrawler error", err)
			return nil, err
		}
	*/

	fiatCurrencyCrawler, err := fiatcurrency.NewFiatCurrencyCrawler(db, config, shutdown)
	if err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return nil, err
	}

	binanceTicker := NewBinanceTickerCrawler(db)

	return &Crawler{
		// ExchangeOrderbook:   exchangeOrderbook,
		FiatCurrencyCrawler: fiatCurrencyCrawler,
		BinanceTicker:       binanceTicker,
		db:                  db,
		redisClient:         redisClient,
	}, nil
}

func (cl *Crawler) Start(ctx context.Context) error {
	/*
		err := cl.ExchangeOrderbook.Start()
		if err != nil {
			log.Error("Crawler ExchangeOrderbook Start error", err)
			return err
		}
	*/
	if cl.FiatCurrencyCrawler != nil {
		if err := cl.FiatCurrencyCrawler.Start(); err != nil {
			log.Error("Crawler FiatCurrencyCrawler error", err)
			return err
		}
	}
	if cl.BinanceTicker != nil {
		if err := cl.BinanceTicker.Start(); err != nil {
			log.Error("Crawler BinanceTicker Start error", err)
			return err
		}
	}
	return nil
}

func (cl *Crawler) Stop(ctx context.Context) error {
	if cl.stopped.Swap(true) {
		return nil
	}

	var result error
	if cl.ExchangeOrderbook != nil {
		if err := cl.ExchangeOrderbook.Close(); err != nil {
			log.Error("Crawler ExchangeOrderbook Stop error", "err", err)
			result = errors.Join(result, err)
		}
	}

	if cl.FiatCurrencyCrawler != nil {
		if err := cl.FiatCurrencyCrawler.Close(); err != nil {
			log.Error("Crawler FiatCurrencyCrawler error", "err", err)
			result = errors.Join(result, err)
		}
	}

	if cl.BinanceTicker != nil {
		if err := cl.BinanceTicker.Stop(); err != nil {
			log.Error("Crawler BinanceTicker Stop error", "err", err)
			result = errors.Join(result, err)
		}
	}
	if cl.redisClient != nil {
		result = errors.Join(result, cl.redisClient.Close())
	}
	if cl.db != nil {
		result = errors.Join(result, cl.db.Close())
	}
	return result
}

func (cl *Crawler) Stopped() bool {
	return cl.stopped.Load()
}
