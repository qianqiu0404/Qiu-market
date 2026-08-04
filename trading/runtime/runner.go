package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/store"
)

var (
	ErrQueueFull          = errors.New("market command queue is full")
	ErrUnavailable        = errors.New("market runner is unavailable")
	ErrClosed             = errors.New("market runner is closed")
	ErrRecoveryInProgress = errors.New("recovery_in_progress")
)

type State string

const (
	StateReady      State = "ready"
	StateRecovering State = "recovering"
	StateFailed     State = "failed"
	StateClosing    State = "closing"
	StateClosed     State = "closed"
)

type Config struct {
	QueueSize       int
	SnapshotEvery   uint64
	SnapshotTimeout time.Duration
	WriteGate       interface {
		RequireWritable(context.Context) error
	}
}

func DefaultConfig() Config {
	return Config{
		QueueSize:       256,
		SnapshotEvery:   100,
		SnapshotTimeout: 2 * time.Minute,
	}
}

type Status struct {
	MarketID        domain.MarketID `json:"market_id"`
	State           State           `json:"state"`
	Sequence        uint64          `json:"sequence"`
	QueueDepth      int             `json:"queue_depth"`
	RecoveryCount   uint64          `json:"recovery_count"`
	LastError       string          `json:"last_error,omitempty"`
	LastIncident    string          `json:"last_incident,omitempty"`
	LastIncidentAt  string          `json:"last_incident_at,omitempty"`
	LastRecoveredAt string          `json:"last_recovered_at,omitempty"`
}

type operation uint8

const (
	operationFund operation = iota + 1
	operationSubmit
	operationCancel
)

type command struct {
	ctx      context.Context
	kind     operation
	fund     domain.FundRequest
	submit   domain.NewOrder
	cancel   domain.CancelOrder
	response chan response
}

type response struct {
	result domain.Result
	err    error
}

// MarketRunner is the single sequential command owner for one market. Once a
// command is accepted into the queue it may still commit after the caller's
// context is canceled; callers must retry with the same idempotency key.
type MarketRunner struct {
	market    domain.Market
	eventLog  store.EventStore
	snapshots store.SnapshotStore
	config    Config

	queue chan command
	stop  chan struct{}
	done  chan struct{}

	gate      sync.RWMutex
	accepting bool
	stopOnce  sync.Once

	mu              sync.RWMutex
	trading         *exchange.Exchange
	state           State
	sequence        uint64
	lastError       string
	lastIncident    string
	lastIncidentAt  time.Time
	lastRecoveredAt time.Time
	recoveryCount   uint64
	closeErr        error
}

func NewMarketRunner(
	ctx context.Context,
	market domain.Market,
	eventLog store.EventStore,
	snapshots store.SnapshotStore,
	config Config,
) (*MarketRunner, error) {
	if config.QueueSize <= 0 {
		return nil, fmt.Errorf("queue size must be positive")
	}
	if config.SnapshotEvery == 0 {
		return nil, fmt.Errorf("snapshot interval must be positive")
	}
	if config.SnapshotTimeout < 0 {
		return nil, fmt.Errorf("snapshot timeout must not be negative")
	}
	if config.SnapshotTimeout == 0 {
		config.SnapshotTimeout = DefaultConfig().SnapshotTimeout
	}
	restored, err := exchange.Restore(ctx, market, eventLog, snapshots)
	if err != nil {
		return nil, fmt.Errorf("restore market %s: %w", market.ID, err)
	}
	runner := &MarketRunner{
		market:    market,
		eventLog:  eventLog,
		snapshots: snapshots,
		config:    config,
		queue:     make(chan command, config.QueueSize),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		accepting: true,
		trading:   restored,
		state:     StateReady,
		sequence:  restored.Sequence(),
	}
	go runner.loop()
	return runner, nil
}

func (r *MarketRunner) Fund(ctx context.Context, request domain.FundRequest) (domain.Result, error) {
	return r.execute(ctx, command{ctx: ctx, kind: operationFund, fund: request})
}

func (r *MarketRunner) Submit(ctx context.Context, request domain.NewOrder) (domain.Result, error) {
	return r.execute(ctx, command{ctx: ctx, kind: operationSubmit, submit: request})
}

func (r *MarketRunner) Cancel(ctx context.Context, request domain.CancelOrder) (domain.Result, error) {
	return r.execute(ctx, command{ctx: ctx, kind: operationCancel, cancel: request})
}

func (r *MarketRunner) Market() (domain.Market, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return domain.Market{}, err
	}
	return trading.Market(), nil
}

func (r *MarketRunner) Balance(accountID domain.AccountID, asset domain.Asset) (exchange.BalanceView, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return exchange.BalanceView{}, err
	}
	return trading.Balance(accountID, asset), nil
}

func (r *MarketRunner) Balances(accountID domain.AccountID) ([]exchange.AssetBalanceView, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return nil, err
	}
	return trading.Balances(accountID), nil
}

func (r *MarketRunner) Order(orderID domain.OrderID) (domain.Order, bool, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return domain.Order{}, false, err
	}
	order, found := trading.Order(orderID)
	return order, found, nil
}

func (r *MarketRunner) Orders(accountID domain.AccountID, openOnly bool) ([]domain.Order, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return nil, err
	}
	return trading.Orders(accountID, openOnly), nil
}

func (r *MarketRunner) Trades(accountID domain.AccountID) ([]domain.Trade, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return nil, err
	}
	return trading.Trades(accountID), nil
}

func (r *MarketRunner) Depth(levels int) (exchange.OrderBookView, error) {
	trading, err := r.readyExchange()
	if err != nil {
		return exchange.OrderBookView{}, err
	}
	return trading.Depth(levels)
}

func (r *MarketRunner) Status() Status {
	r.mu.RLock()
	state := r.state
	lastError := r.lastError
	lastIncident := r.lastIncident
	lastIncidentAt := r.lastIncidentAt
	lastRecoveredAt := r.lastRecoveredAt
	recoveryCount := r.recoveryCount
	sequence := r.sequence
	r.mu.RUnlock()

	return Status{
		MarketID:        r.market.ID,
		State:           state,
		Sequence:        sequence,
		QueueDepth:      len(r.queue),
		RecoveryCount:   recoveryCount,
		LastError:       lastError,
		LastIncident:    lastIncident,
		LastIncidentAt:  formatStatusTime(lastIncidentAt),
		LastRecoveredAt: formatStatusTime(lastRecoveredAt),
	}
}

func (r *MarketRunner) Close(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.gate.Lock()
		r.accepting = false
		r.setState(StateClosing, "")
		close(r.stop)
		r.gate.Unlock()
	})
	select {
	case <-r.done:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *MarketRunner) execute(ctx context.Context, request command) (domain.Result, error) {
	if ctx == nil {
		return domain.Result{}, fmt.Errorf("context is required")
	}
	if r.config.WriteGate != nil {
		if err := r.config.WriteGate.RequireWritable(ctx); err != nil {
			return domain.Result{}, fmt.Errorf("%w: %v", ErrRecoveryInProgress, err)
		}
	}
	request.response = make(chan response, 1)

	r.gate.RLock()
	if !r.accepting {
		r.gate.RUnlock()
		if r.Status().State == StateClosed {
			return domain.Result{}, ErrClosed
		}
		return domain.Result{}, ErrUnavailable
	}
	select {
	case r.queue <- request:
		r.gate.RUnlock()
	case <-ctx.Done():
		r.gate.RUnlock()
		return domain.Result{}, ctx.Err()
	default:
		r.gate.RUnlock()
		return domain.Result{}, ErrQueueFull
	}

	select {
	case reply := <-request.response:
		return reply.result, reply.err
	case <-ctx.Done():
		return domain.Result{}, ctx.Err()
	}
}

func (r *MarketRunner) loop() {
	defer close(r.done)
	for {
		select {
		case request := <-r.queue:
			r.handle(request)
		case <-r.stop:
			r.drainAndClose()
			return
		}
	}
}

func (r *MarketRunner) drainAndClose() {
	for {
		select {
		case request := <-r.queue:
			r.handle(request)
		default:
			r.mu.RLock()
			trading := r.trading
			r.mu.RUnlock()
			ctx, cancel := context.WithTimeout(context.Background(), r.config.SnapshotTimeout)
			_, err := trading.SaveSnapshot(ctx)
			cancel()
			r.mu.Lock()
			r.closeErr = err
			if err != nil {
				r.recordIncidentLocked(err)
			}
			r.state = StateClosed
			r.mu.Unlock()
			return
		}
	}
}

func (r *MarketRunner) handle(request command) {
	r.mu.RLock()
	trading := r.trading
	state := r.state
	r.mu.RUnlock()
	if state != StateReady && state != StateClosing {
		request.response <- response{err: ErrUnavailable}
		return
	}

	before := trading.Sequence()
	var result domain.Result
	var err error
	switch request.kind {
	case operationFund:
		result, err = trading.Fund(request.ctx, request.fund)
	case operationSubmit:
		result, err = trading.Submit(request.ctx, request.submit)
	case operationCancel:
		result, err = trading.Cancel(request.ctx, request.cancel)
	default:
		err = fmt.Errorf("unsupported runner operation")
	}
	if errors.Is(err, exchange.ErrPersistence) {
		r.recoverAfterPersistenceError(err)
		request.response <- response{result: result, err: err}
		return
	}
	if err == nil {
		after := trading.Sequence()
		r.mu.Lock()
		r.sequence = after
		r.mu.Unlock()
		if after > before && after%r.config.SnapshotEvery == 0 {
			snapshotContext, cancel := context.WithTimeout(
				context.Background(),
				r.config.SnapshotTimeout,
			)
			_, snapshotErr := trading.SaveSnapshot(snapshotContext)
			cancel()
			if snapshotErr != nil {
				r.setLastError(fmt.Errorf("periodic snapshot: %w", snapshotErr))
			} else {
				r.clearLastError()
			}
		}
	}
	request.response <- response{result: result, err: err}
}

func (r *MarketRunner) recoverAfterPersistenceError(cause error) {
	r.gate.Lock()
	r.accepting = false
	r.mu.Lock()
	r.state = StateRecovering
	r.recordIncidentLocked(cause)
	r.mu.Unlock()
	r.gate.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	restored, err := exchange.Restore(ctx, r.market, r.eventLog, r.snapshots)
	cancel()
	if err != nil {
		r.gate.Lock()
		r.mu.Lock()
		r.state = StateFailed
		r.recordIncidentLocked(fmt.Errorf("recover after persistence failure: %w", err))
		r.mu.Unlock()
		r.gate.Unlock()
		return
	}

	r.gate.Lock()
	r.mu.Lock()
	r.trading = restored
	r.sequence = restored.Sequence()
	r.lastError = ""
	r.lastRecoveredAt = time.Now().UTC()
	r.recoveryCount++
	select {
	case <-r.stop:
		r.state = StateClosing
	default:
		r.state = StateReady
		r.accepting = true
	}
	r.mu.Unlock()
	r.gate.Unlock()
}

func (r *MarketRunner) readyExchange() (*exchange.Exchange, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state != StateReady || r.trading == nil {
		if r.state == StateClosed {
			return nil, ErrClosed
		}
		return nil, ErrUnavailable
	}
	return r.trading, nil
}

func (r *MarketRunner) setState(state State, lastError string) {
	r.mu.Lock()
	r.state = state
	r.lastError = lastError
	r.mu.Unlock()
}

func (r *MarketRunner) setLastError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.recordIncidentLocked(err)
	r.mu.Unlock()
}

func (r *MarketRunner) clearLastError() {
	r.mu.Lock()
	r.lastError = ""
	r.mu.Unlock()
}

func (r *MarketRunner) recordIncidentLocked(err error) {
	if err == nil {
		return
	}
	message := err.Error()
	r.lastError = message
	r.lastIncident = message
	r.lastIncidentAt = time.Now().UTC()
}

func formatStatusTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
