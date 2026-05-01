package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
)

type MarketPriceStore interface {
	Get(ctx context.Context, key string) (string, error)
}

type MarketPriceHandle struct {
	db             *database.DB
	redisCli       MarketPriceStore
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewMarketPriceHandle(db *database.DB, redisClient MarketPriceStore, shutdown context.CancelCauseFunc) (*MarketPriceHandle, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	return &MarketPriceHandle{
		db:             db,
		redisCli:       redisClient,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("market price handle critical error: %v", err))
		}},
	}, nil
}

func (mph *MarketPriceHandle) Close() error {
	mph.resourceCancel()
	return mph.tasks.Wait()
}

func (mph *MarketPriceHandle) Start() error {
	mph.tasks.Go(func() error {
		tickerOperator := time.NewTicker(time.Second * 5)
		defer tickerOperator.Stop()
		for {
			select {
			case <-tickerOperator.C:
				err := mph.onPriceData()
				if err != nil {
					log.Error("market price handle fail", "error", err)
				}
			case <-mph.resourceCtx.Done():
				log.Info("market price handle shutting down")
				return errors.New("market price service stopped")
			}
		}
	})
	return nil
}

func (mph *MarketPriceHandle) onPriceData() error {
	exchangeList, err := mph.db.Exchange.QueryExchanges()
	if err != nil {
		log.Error("Query exchanges fail", "error", err)
		return err
	}
	for _, exchange := range exchangeList {
		exchangeSymbols, err := mph.db.ExchangeSymbol.QuerySymbolsByExchangeId(exchange.Guid)
		if err != nil {
			return err
		}
		for _, exchangeSymbol := range exchangeSymbols {
			symbol, err := mph.db.Symbol.QuerySymbolByGuid(exchangeSymbol.SymbolGuid)
			if err != nil {
				log.Error("Query symbol fail", "error", err)
				return err
			}

			key := marketkey.Build(exchange.Guid, exchange.Name, symbol.Guid, symbol.SymbolName)
			
			avgPrice, err := mph.redisCli.Get(mph.resourceCtx, key)
			if err != nil {
				log.Debug("Get avgPrice fail", "key", key, "error", err)
				continue
			}
			
			askPrice, _ := mph.redisCli.Get(mph.resourceCtx, key+"askPrice")
			bidPrice, _ := mph.redisCli.Get(mph.resourceCtx, key+"bidPrice")
			volume, _ := mph.redisCli.Get(mph.resourceCtx, key+"volume")
			if volume == "" {
				volume = "0"
			}

			guid, _ := uuid.NewUUID()
			radio := strconv.FormatFloat(mph.calcRate(avgPrice), 'f', 4, 64)
			
			dataSymbolMk := &database.SymbolMarket{
				Guid:       guid.String(),
				SymbolGuid: symbol.Guid,
				Price:      avgPrice,
				AskPrice:   askPrice,
				BidPrice:   bidPrice,
				Volume:     volume,
				MarketCap:  "0", // Simplified
				Radio:      radio,
				IsActive:   true,
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}

			err = mph.db.SymbolMarket.StoreSymbolMarket(dataSymbolMk)
			if err != nil {
				log.Error("Store symbol market fail", "error", err)
				return err
			}
		}
	}
	return nil
}

func (mph *MarketPriceHandle) calcRate(currentPriceStr string) float64 {
	marketDataPrice, err := mph.db.SymbolMarket.QuerySymbolMarketTodayFirstData()
	if err != nil {
		// If no data today, rate is 0
		return 0
	}
	
	startOfDayPrice, _ := strconv.ParseFloat(marketDataPrice.Price, 64)
	currentPrice, _ := strconv.ParseFloat(currentPriceStr, 64)
	
	if startOfDayPrice == 0 {
		return 0
	}
	
	return (currentPrice - startOfDayPrice) / startOfDayPrice
}

func (mph *MarketPriceHandle) Stop() error {
	mph.resourceCancel()
	return mph.tasks.Wait()
}
