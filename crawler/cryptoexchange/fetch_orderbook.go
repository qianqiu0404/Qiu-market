package cryptoexchange

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type ExchangeOrderbook struct {
	db             *database.DB
	redisCli       *redis.Client
	exchangeClient *ExchangeClient
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewExchangeOrderbook(db *database.DB, redisClient *redis.Client, shutdown context.CancelCauseFunc) (*ExchangeOrderbook, error) {
	exchangeClient, err := NewExchangeClient("http://127.0.0.1:7890", "http")
	if err != nil {
		log.Error("Failed to create exchange client")
		return nil, err
	}
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()
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
		for {
			tickerOperator := time.NewTicker(time.Second * 5)
			defer tickerOperator.Stop()
			select {
			case <-tickerOperator.C:
				log.Debug("Fetching order book start")
				err := bc.syncOrderBookData()
				if err != nil {
					log.Error("sync order book data fail", "error", err)
					return err
				}
			case <-bc.resourceCtx.Done():
				log.Info("exchange fetch orderbook shutting down")
				return errors.New("exchange stopped")
			}
		}
	})
	return nil
}

func (bc *ExchangeOrderbook) syncOrderBookData() error {
	exchangeList, err := bc.db.Exchange.QueryExchanges()
	if err != nil {
		log.Error("Query exchanges fail", "error", err)
		return err
	}
	for _, exchange := range exchangeList {
		log.Info("exchange", "exchange", exchange.Name)
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
			log.Info("symbol", "symbolName", symbol.SymbolName)

			orderBook, err := bc.exchangeClient.FetchOrderBook(exchange.Name, symbol.SymbolName)
			if err != nil {
				log.Error("Fetch order book fail", "symbol", symbol.SymbolName, "error", err)
				return err
			}
			askPrice := orderBook.Asks[0][0]
			bidPrice := orderBook.Bids[0][0]
			avgPrice := (askPrice + bidPrice) / 2
			key := exchange.Guid + "%" + exchange.Name + "%" + symbol.Guid + "%" + symbol.SymbolName

			log.Info("Fetch orderbook success", "key", key, "askPrice", askPrice, "bidPrice", bidPrice, "avgPrice", avgPrice)

			err = bc.redisCli.Set(bc.resourceCtx, key, avgPrice, time.Second*600)
			if err != nil {
				log.Error("Set avgPrice fail", "symbol", symbol.SymbolName, "error", err)
				return err
			}
			askPriceKey := key + "askPrice"
			err = bc.redisCli.Set(bc.resourceCtx, askPriceKey, askPrice, time.Second*600)
			if err != nil {
				log.Error("Set askPrice fail", "symbol", symbol.SymbolName, "error", err)
				return err
			}
			bidPriceKey := key + "bidPrice"
			err = bc.redisCli.Set(bc.resourceCtx, bidPriceKey, bidPrice, time.Second*600)
			if err != nil {
				log.Error("Set askPrice fail", "symbol", symbol.SymbolName, "error", err)
				return err
			}
		}
	}
	return nil
}

func (bc *ExchangeOrderbook) Stop() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}
