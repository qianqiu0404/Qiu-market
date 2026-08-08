package cryptoexchange

import (
	"context"
	"fmt"
	"time"

	"github.com/ccxt/ccxt/go/v4"
	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type ExchangeMarketClient interface {
	FetchOrderBook(exchangeName, symbol string) (*ccxt.OrderBook, error)
	FetchTicker(exchangeName, symbol string) (*ccxt.Ticker, error)
}

type ExchangeOrderbook struct {
	db             *database.DB
	redisCli       *redis.Client
	exchangeClient ExchangeMarketClient
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewExchangeOrderbook(db *database.DB, redisClient *redis.Client, shutdown context.CancelCauseFunc) (*ExchangeOrderbook, error) {
	exchangeClient, err := NewExchangeClient("", "")
	if err != nil {
		log.Error("Failed to create exchange client")
		return nil, err
	}
	return NewExchangeOrderbookWithClient(db, redisClient, exchangeClient, shutdown)
}

func NewExchangeOrderbookWithClient(db *database.DB, redisClient *redis.Client, exchangeClient ExchangeMarketClient, shutdown context.CancelCauseFunc) (*ExchangeOrderbook, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	return &ExchangeOrderbook{
		db:             db,
		redisCli:       redisClient,
		exchangeClient: exchangeClient,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("exchange crawler critical error: %v", err))
		}},
	}, nil
}

func (bc *ExchangeOrderbook) Close() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}

func (bc *ExchangeOrderbook) Start() error {
	bc.tasks.Go(func() error {
		tickerOperator := time.NewTicker(time.Second * 5)
		defer tickerOperator.Stop()
		for {
			select {
			case <-tickerOperator.C:
				log.Debug("Fetching market data start")
				err := bc.syncMarketData()
				if err != nil {
					log.Error("sync market data fail", "error", err)
				}
			case <-bc.resourceCtx.Done():
				log.Info("exchange fetcher shutting down")
				return nil
			}
		}
	})
	return nil
}

func (bc *ExchangeOrderbook) syncMarketData() error {
	exchangeList, err := bc.db.Exchange.QueryExchanges()
	if err != nil {
		log.Error("Query exchanges fail", "error", err)
		return err
	}
	for _, exchange := range exchangeList {
		exchangeSymbols, err := bc.db.ExchangeSymbol.QuerySymbolsByExchangeId(exchange.Guid)
		if err != nil {
			return err
		}
		for _, exchangeSymbol := range exchangeSymbols {
			symbol, err := bc.db.Symbol.QuerySymbolByGuid(exchangeSymbol.SymbolGuid)
			if err != nil {
				log.Error("Query symbol fail", "error", err)
				return err
			}

			ticker, err := bc.exchangeClient.FetchTicker(exchange.Name, symbol.SymbolName)
			if err != nil {
				log.Error("Fetch ticker fail", "symbol", symbol.SymbolName, "error", err)
				continue
			}

			lastPrice := ticker.Last
			volume := ticker.BaseVolume
			askPrice := ticker.Ask
			bidPrice := ticker.Bid

			key := marketkey.Build(exchange.Guid, exchange.Name, symbol.Guid, symbol.SymbolName)
			log.Info("Fetch ticker success", "key", key, "price", lastPrice, "volume", volume)

			err = bc.redisCli.Set(bc.resourceCtx, key, formatOptionalFloat(lastPrice), time.Second*600)
			if err != nil {
				log.Error("Set lastPrice fail", "symbol", symbol.SymbolName, "error", err)
			}
			bc.redisCli.Set(bc.resourceCtx, key+"askPrice", formatOptionalFloat(askPrice), time.Second*600)
			bc.redisCli.Set(bc.resourceCtx, key+"bidPrice", formatOptionalFloat(bidPrice), time.Second*600)
			bc.redisCli.Set(bc.resourceCtx, key+"volume", formatOptionalFloat(volume), time.Second*600)
		}
	}
	return nil
}

func formatOptionalFloat(value *float64) string {
	if value == nil {
		return "0"
	}
	return fmt.Sprintf("%f", *value)
}

func (bc *ExchangeOrderbook) Stop() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}
