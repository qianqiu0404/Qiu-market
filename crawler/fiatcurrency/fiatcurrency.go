package fiatcurrency

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
)

type FiatCurrencyCrawler struct {
	db                 *database.DB
	exchangeRateWorker *ExchangeRateWorker
	resourceCtx        context.Context
	resourceCancel     context.CancelFunc
	tasks              tasks.Group
}

func NewFiatCurrencyCrawler(db *database.DB, conf *config.Config, shutdown context.CancelCauseFunc) (*FiatCurrencyCrawler, error) {
	providerConfigs := map[string]string{
		"ExchangeRate-API":  conf.APIKeyConfig.ExchangeRate,
		"Fixer.io":          conf.APIKeyConfig.FixerIO,
		"OpenExchangeRates": conf.APIKeyConfig.OpenExchangeRates,
		"CurrencyAPI":       conf.APIKeyConfig.Currency,
		"CurrencyBeacon":    conf.APIKeyConfig.CurrencyBeacon,
		"FawazExchange":     "", // No API key needed
		"CurrencyFreaks":    conf.APIKeyConfig.CurrencyFreaks,
	}

	strategyConfigs := BuildStrategyConfigs(conf.ExchangeRatePlatforms)

	exchangeRateWorker, err := NewExchangeRateWorker(db, conf, providerConfigs, strategyConfigs)
	if err != nil {
		log.Error("NewExchangeRateWorker error", "err", err)
		return nil, err
	}

	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()
	return &FiatCurrencyCrawler{
		db:                 db,
		exchangeRateWorker: exchangeRateWorker,
		resourceCtx:        resourceCtx,
		resourceCancel:     resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("Fiat Currency crawler critical error: %v", err))
		}},
	}, nil
}

func (bc *FiatCurrencyCrawler) Close() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}

func (bc *FiatCurrencyCrawler) Start() error {
	bc.tasks.Go(func() error {
		for {
			tickerOperator := time.NewTicker(time.Second * 5)
			defer tickerOperator.Stop()
			select {
			case <-tickerOperator.C:
				bc.exchangeRateWorker.FetchAndStoreRates()
			case <-bc.resourceCtx.Done():
				log.Info("Fiat Currency shutting down")
				return errors.New("Fiat Currency stopped")
			}
		}
	})
	return nil
}
