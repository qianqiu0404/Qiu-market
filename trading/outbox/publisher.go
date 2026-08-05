package outbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

type Store interface {
	OutboxCheckpoint(context.Context) (postgresstore.Cursor, bool, error)
	PublishOutboxBatch(context.Context, int) (postgresstore.PublishResult, error)
	CleanupPublishedOutbox(context.Context, time.Time, int) (int64, error)
}

type Config struct {
	BatchSize          int
	PollEvery          time.Duration
	MaximumRetryDelay  time.Duration
	CleanupEvery       time.Duration
	PublishedRetention time.Duration
	CleanupBatchSize   int
	MaximumCleanupRuns int
}

func DefaultConfig() Config {
	return Config{
		BatchSize:          500,
		PollEvery:          250 * time.Millisecond,
		MaximumRetryDelay:  5 * time.Second,
		CleanupEvery:       time.Hour,
		PublishedRetention: 24 * time.Hour,
		CleanupBatchSize:   1_000,
		MaximumCleanupRuns: 10,
	}
}

type Status struct {
	State              string
	Checkpoint         postgresstore.Cursor
	LastError          string
	LastIncident       string
	LastIncidentAt     time.Time
	LastPublishedAt    time.Time
	LastCleanupAt      time.Time
	PublishedSinceBoot uint64
	CleanedSinceBoot   uint64
}

type Publisher struct {
	store  Store
	config Config

	mu              sync.RWMutex
	status          Status
	cleanupDegraded bool
}

func New(store Store, config Config) (*Publisher, error) {
	if store == nil {
		return nil, fmt.Errorf("outbox store is required")
	}
	if config.BatchSize <= 0 || config.BatchSize > 1_000 {
		return nil, fmt.Errorf("outbox batch size must be between 1 and 1000")
	}
	if config.PollEvery <= 0 ||
		config.MaximumRetryDelay < config.PollEvery ||
		config.CleanupEvery <= 0 ||
		config.PublishedRetention <= 0 ||
		config.CleanupBatchSize <= 0 ||
		config.CleanupBatchSize > 10_000 ||
		config.MaximumCleanupRuns <= 0 {
		return nil, fmt.Errorf("invalid outbox publisher timing or cleanup configuration")
	}
	return &Publisher{
		store:  store,
		config: config,
		status: Status{State: "starting"},
	}, nil
}

func (p *Publisher) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

func (p *Publisher) Run(ctx context.Context) {
	retryDelay := p.config.PollEvery
	nextCleanup := time.Now().UTC().Add(p.config.CleanupEvery)
	delay := time.Duration(0)
	initialized := false
	for {
		if !wait(ctx, delay) {
			p.setStopped()
			return
		}

		if !initialized {
			checkpoint, _, err := p.store.OutboxCheckpoint(ctx)
			if err != nil {
				if ctx.Err() != nil {
					p.setStopped()
					return
				}
				p.recordError(fmt.Errorf("load outbox checkpoint: %w", err))
				delay = retryDelay
				retryDelay = minDuration(retryDelay*2, p.config.MaximumRetryDelay)
				continue
			}
			p.mu.Lock()
			p.status.Checkpoint = checkpoint
			p.mu.Unlock()
			initialized = true
		}

		result, err := p.store.PublishOutboxBatch(ctx, p.config.BatchSize)
		if err != nil {
			if ctx.Err() != nil {
				p.setStopped()
				return
			}
			p.recordError(fmt.Errorf("publish outbox: %w", err))
			delay = retryDelay
			retryDelay = minDuration(retryDelay*2, p.config.MaximumRetryDelay)
			continue
		}
		retryDelay = p.config.PollEvery
		p.recordPublish(result)

		now := time.Now().UTC()
		if !now.Before(nextCleanup) {
			if err := p.cleanup(ctx, now); err != nil {
				if ctx.Err() != nil {
					p.setStopped()
					return
				}
				p.recordCleanupError(err)
				nextCleanup = now.Add(minDuration(time.Minute, p.config.CleanupEvery))
			} else {
				nextCleanup = now.Add(p.config.CleanupEvery)
			}
		}

		if result.Published == p.config.BatchSize {
			delay = 0
		} else {
			delay = p.config.PollEvery
		}
	}
}

func (p *Publisher) cleanup(ctx context.Context, now time.Time) error {
	var total int64
	for run := 0; run < p.config.MaximumCleanupRuns; run++ {
		deleted, err := p.store.CleanupPublishedOutbox(
			ctx,
			now.Add(-p.config.PublishedRetention),
			p.config.CleanupBatchSize,
		)
		if err != nil {
			return fmt.Errorf("cleanup published outbox: %w", err)
		}
		total += deleted
		if deleted < int64(p.config.CleanupBatchSize) {
			break
		}
	}
	p.mu.Lock()
	p.status.LastCleanupAt = now
	p.status.CleanedSinceBoot += uint64(total)
	p.cleanupDegraded = false
	p.status.State = "ready"
	p.status.LastError = ""
	p.mu.Unlock()
	return nil
}

func (p *Publisher) recordPublish(result postgresstore.PublishResult) {
	now := time.Now().UTC()
	p.mu.Lock()
	if !p.cleanupDegraded {
		p.status.State = "ready"
		p.status.LastError = ""
	}
	if result.Published > 0 {
		p.status.Checkpoint = result.Checkpoint
		p.status.LastPublishedAt = now
		p.status.PublishedSinceBoot += uint64(result.Published)
	}
	p.mu.Unlock()
}

func (p *Publisher) recordCleanupError(err error) {
	p.mu.Lock()
	p.cleanupDegraded = true
	p.mu.Unlock()
	p.recordError(err)
}

func (p *Publisher) recordError(err error) {
	now := time.Now().UTC()
	p.mu.Lock()
	p.status.State = "degraded"
	p.status.LastError = err.Error()
	p.status.LastIncident = err.Error()
	p.status.LastIncidentAt = now
	p.mu.Unlock()
}

func (p *Publisher) setStopped() {
	p.mu.Lock()
	p.status.State = "stopped"
	p.mu.Unlock()
}

func wait(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
