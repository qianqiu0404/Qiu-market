package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type MarketPriceHandle struct {
	db             *database.DB
	redisCli       *redis.Client
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewMarketPriceHandle(db *database.DB, redisClient *redis.Client, shutdown context.CancelCauseFunc) (*MarketPriceHandle, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	defer resourceCancel()
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
		for {
			tickerOperator := time.NewTicker(time.Second * 5)
			defer tickerOperator.Stop()
			select {
			case <-tickerOperator.C:
				err := mph.onPriceData()
				if err != nil {
					log.Error("market price handle fail", "error", err)
					return err
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

			key := exchange.Guid + "%" + exchange.Name + "%" + symbol.Guid + "%" + symbol.SymbolName
			avgPrice, _ := mph.redisCli.Get(mph.resourceCtx, key)
			askPriceKey := key + "askPrice"
			askPrice, _ := mph.redisCli.Get(mph.resourceCtx, askPriceKey)
			bidPriceKey := key + "bidPrice"
			bidPrice, _ := mph.redisCli.Get(mph.resourceCtx, bidPriceKey)
			guid, _ := uuid.NewUUID()
			radio := strconv.FormatFloat(mph.calcRate(avgPrice), 'f', 2, 64)
			dataSymbolMk := &database.SymbolMarket{
				Guid:       guid.String(),
				SymbolGuid: symbol.Guid,
				Price:      avgPrice,
				AskPrice:   askPrice,
				BidPrice:   bidPrice,
				Volume:     "10000",
				MarketCap:  "10000",
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

func (mph *MarketPriceHandle) calcRate(askPrice string) float64 {
	marketDataPrice, err := mph.db.SymbolMarket.QuerySymbolMarketTodayFirstData()
	if err != nil {
		log.Error("Query symbol market data fail", "error", err)
		return 0
	}
	startOfDayPrice := marketDataPrice.Price
	currentPrice, _ := strconv.ParseFloat(askPrice, 64)
	fistPrice, _ := strconv.ParseFloat(startOfDayPrice, 64)
	radio := currentPrice - fistPrice/fistPrice
	return radio
}

func StringToFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func (mph *MarketPriceHandle) Stop() error {
	mph.resourceCancel()
	return mph.tasks.Wait()
}
