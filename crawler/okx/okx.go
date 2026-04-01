package okx

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type OkxCrawler struct {
	db             *database.DB
	redisCli       *redis.Client
	resourceCtx    context.Context
	resourceCancel context.CancelFunc
	tasks          tasks.Group
}

func NewOkxCrawler(db *database.DB, redisCli *redis.Client, shutdown context.CancelCauseFunc) (*OkxCrawler, error) {
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	return &OkxCrawler{
		db:             db,
		redisCli:       redisCli,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("OkxCrawler critical error: %v", err))
		}},
	}, nil
}

func (oc *OkxCrawler) Close() error {
	oc.resourceCancel()
	return oc.tasks.Wait()
}

func (oc *OkxCrawler) Start() error {
	oc.tasks.Go(func() error {
		tickerOperator := time.NewTicker(time.Second * 5)
		defer tickerOperator.Stop()
		for {
			select {
			case <-tickerOperator.C:
				log.Println("okx orderbook start")
			case <-oc.resourceCtx.Done():
				log.Println("okx orderbook stopped")
				return errors.New("okx orderbook stopped")
			}
		}
	})
	return nil
}
