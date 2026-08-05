package crawler

import (
	"context"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/crawler/fiatcurrency"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Crawler struct {
	FiatCurrencyCrawler  *fiatcurrency.FiatCurrencyCrawler
	CatalogSupervisor    *CatalogSupervisor
	SpotTickerSupervisor *SpotTickerSupervisor
	CEXKlineSupervisor   *CEXKlineSupervisor
	stopped              atomic.Bool
}

func NewCrawler(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Crawler, error) {
	fiatCurrencyCrawler, err := fiatcurrency.NewFiatCurrencyCrawler(db, config, shutdown)
	if err != nil {
		log.Error("Crawler FiatCurrencyCrawler error", err)
		return nil, err
	}

	catalogSupervisor := NewCatalogSupervisor(db, config.MultiVenueEnabled)
	spotTickerSupervisor := NewSpotTickerSupervisor(db, redisClient, config.MultiVenueEnabled)
	cexKlineSupervisor := NewCEXKlineSupervisor(db)

	return &Crawler{
		FiatCurrencyCrawler:  fiatCurrencyCrawler,
		CatalogSupervisor:    catalogSupervisor,
		SpotTickerSupervisor: spotTickerSupervisor,
		CEXKlineSupervisor:   cexKlineSupervisor,
	}, nil
}

func (cl *Crawler) Start(ctx context.Context) error {
	if cl.FiatCurrencyCrawler != nil {
		if err := cl.FiatCurrencyCrawler.Start(); err != nil {
			log.Error("Crawler FiatCurrencyCrawler error", err)
			return err
		}
	}
	if cl.CatalogSupervisor != nil {
		if err := cl.CatalogSupervisor.Start(ctx); err != nil {
			log.Error("Crawler CatalogSupervisor Start error", err)
			return err
		}
	}
	if cl.SpotTickerSupervisor != nil {
		if err := cl.SpotTickerSupervisor.Start(ctx); err != nil {
			log.Error("Crawler SpotTickerSupervisor Start error", err)
			return err
		}
	}
	if cl.CEXKlineSupervisor != nil {
		if err := cl.CEXKlineSupervisor.Start(ctx); err != nil {
			log.Error("Crawler CEXKlineSupervisor Start error", err)
			return err
		}
	}
	return nil
}

func (cl *Crawler) Stop(ctx context.Context) error {
	if cl.stopped.Swap(true) {
		return nil
	}

	if cl.FiatCurrencyCrawler != nil {
		if err := cl.FiatCurrencyCrawler.Close(); err != nil {
			log.Error("Crawler FiatCurrencyCrawler error", err)
			return err
		}
	}

	if cl.CatalogSupervisor != nil {
		cl.CatalogSupervisor.Stop()
	}
	if cl.SpotTickerSupervisor != nil {
		cl.SpotTickerSupervisor.Stop()
	}
	if cl.CEXKlineSupervisor != nil {
		cl.CEXKlineSupervisor.Stop()
	}
	return nil
}

func (cl *Crawler) Stopped() bool {
	return cl.stopped.Load()
}
