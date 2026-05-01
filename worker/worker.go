package worker

import (
	"context"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Worker struct {
	marketPriceHandle *MarketPriceHandle
	stopped           atomic.Bool
}

func NewWorker(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Worker, error) {
	marketPriceHandle, err := NewMarketPriceHandle(db, redisClient, shutdown)
	if err != nil {
		return nil, err
	}
	return &Worker{
		marketPriceHandle: marketPriceHandle,
	}, nil
}

func (w *Worker) Start(ctx context.Context) error {
	log.Info("Starting worker")
	if w.marketPriceHandle != nil {
		if err := w.marketPriceHandle.Start(); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) Stop(ctx context.Context) error {
	log.Info("Stopping worker")
	if w.marketPriceHandle != nil {
		if err := w.marketPriceHandle.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) Stopped() bool {
	return w.stopped.Load()
}
