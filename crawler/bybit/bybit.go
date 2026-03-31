package bybit

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
)

type BybitCrawler struct {
	db             *database.DB
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewBybitCrawler(db *database.DB, shutdown context.CancelCauseFunc) (*BybitCrawler, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	return &BybitCrawler{
		db:             db,
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
				log.Println("bybit fetch data start")
			case <-bc.resourceCtx.Done():
				log.Println("bybit fetch data stopped")
				return errors.New("bybit stopped")
			}
		}
	})
	return nil
}
