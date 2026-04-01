package bybit

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

type BybitCrawler struct {
	db             *database.DB
	redisCli       *redis.Client
	bybitCient     *BybitClient
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewBybitCrawler(db *database.DB, redisClient *redis.Client, shutdown context.CancelCauseFunc) (*BybitCrawler, error) {
	bybitCient, err := NewBybitClient()
	if err != nil {
		log.Error("Failed to create Bybit client")
		return nil, err
	}
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()
	return &BybitCrawler{
		db:             db,
		redisCli:       redisClient,
		bybitCient:     bybitCient,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("Bybit crawler critical error: %v", err))
		}},
	}, nil
}

func (bc *BybitCrawler) Close() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}

func (bc *BybitCrawler) Start() error {
	bc.tasks.Go(func() error {
		for {
			tickerOperator := time.NewTicker(time.Second * 5)
			defer tickerOperator.Stop()
			select {
			case <-tickerOperator.C:
				err := bc.syncOrderBookData()
				if err != nil {
					log.Error("sync order book data fail", "error", err)
					return err
				}
			case <-bc.resourceCtx.Done():
				log.Info("Bybit crawler shutting down")
				return errors.New("bybit stopped")
			}
		}
	})
	return nil
}

func (bc *BybitCrawler) syncOrderBookData() error {
	symbolList, err := bc.db.Symbol.QuerySymbols()
	if err != nil {
		log.Error("Query symbols fail", "error", err)
		return err
	}
	for _, symbol := range symbolList {
		orderBook, err := bc.bybitCient.FetchOrderBook(symbol.SymbolName)
		if err != nil {
			log.Error("Fetch order book fail", "symbol", symbol.SymbolName, "error", err)
			return err
		}
		err = bc.redisCli.Set(bc.resourceCtx, symbol.SymbolName, orderBook, time.Second*600)
		if err != nil {
			log.Error("Set order book fail", "symbol", symbol.SymbolName, "error", err)
			return err
		}
	}
	return nil
}
