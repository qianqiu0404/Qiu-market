package binance

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
)

type BinanceCrawler struct {
	db             *database.DB
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewBinanceCrawler(db *database.DB, shutdown context.CancelCauseFunc) (*BinanceCrawler, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	return &BinanceCrawler{
		db:             db,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("binance orderbook critical error: %v", err))
		}},
	}, nil
}

func (bc *BinanceCrawler) Close() error {
	bc.resourceCancel()
	return bc.tasks.Wait()
}

func (bc *BinanceCrawler) Start() error {
	bc.tasks.Go(func() error {
		for {
			tickerOperator := time.NewTicker(time.Second * 5)
			defer tickerOperator.Stop()
			select {
			case <-tickerOperator.C:
				log.Println("binance fetch data start")
			case <-bc.resourceCtx.Done():
				log.Println("binance fetch data  stopped")
				return errors.New("binance stopped")
			}
		}
	})
	return nil
}
