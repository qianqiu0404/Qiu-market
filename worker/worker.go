package worker

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

type Worker struct {
	marketPriceHandle *MarketPriceHandle
	db                *database.DB
	redisClient       *redis.Client
	stopped           atomic.Bool
}

func NewWorker(db *database.DB, redisClient *redis.Client, config *config.Config, shutdown context.CancelCauseFunc) (*Worker, error) {
	marketPriceHandle, err := NewMarketPriceHandle(db, redisClient, shutdown)
	if err != nil {
		return nil, err
	}
	return &Worker{
		marketPriceHandle: marketPriceHandle,
		db:                db,
		redisClient:       redisClient,
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
	if w.stopped.Swap(true) {
		return nil
	}
	log.Info("Stopping worker")
	var result error
	if w.marketPriceHandle != nil {
		if err := w.marketPriceHandle.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}
	if w.redisClient != nil {
		result = errors.Join(result, w.redisClient.Close())
	}
	if w.db != nil {
		result = errors.Join(result, w.db.Close())
	}
	return result
}

func (w *Worker) Stopped() bool {
	return w.stopped.Load()
}
