package goldenpath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

// runnerFacade lets the production RPC server keep one stable Engine while a
// restart atomically replaces the underlying runner restored from persistence.
type runnerFacade struct {
	mu     sync.RWMutex
	market domain.Market
	store  *store.Memory
	config tradingruntime.Config
	runner *tradingruntime.MarketRunner
}

func newRunnerFacade(market domain.Market, memory *store.Memory, runner *tradingruntime.MarketRunner) *runnerFacade {
	return &runnerFacade{market: market, store: memory, config: tradingruntime.DefaultConfig(), runner: runner}
}

func (f *runnerFacade) Submit(ctx context.Context, order domain.NewOrder) (domain.Result, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Submit(ctx, order)
}
func (f *runnerFacade) Cancel(ctx context.Context, order domain.CancelOrder) (domain.Result, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Cancel(ctx, order)
}
func (f *runnerFacade) Fund(ctx context.Context, request domain.FundRequest) (domain.Result, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Fund(ctx, request)
}
func (f *runnerFacade) Market() (domain.Market, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Market()
}
func (f *runnerFacade) Order(id domain.OrderID) (domain.Order, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Order(id)
}
func (f *runnerFacade) Orders(account domain.AccountID, openOnly bool) ([]domain.Order, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Orders(account, openOnly)
}
func (f *runnerFacade) Trades(account domain.AccountID) ([]domain.Trade, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Trades(account)
}
func (f *runnerFacade) Balances(account domain.AccountID) ([]exchange.AssetBalanceView, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Balances(account)
}
func (f *runnerFacade) Depth(levels int) (exchange.OrderBookView, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Depth(levels)
}
func (f *runnerFacade) Status() tradingruntime.Status {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.runner.Status()
}

type restartCapture struct {
	Sequence   uint64
	StateHash  string
	Order      domain.Order
	OrderFound bool
	Trades     []domain.Trade
	Balances   []exchange.AssetBalanceView
	RecordHash string
}

type RestartProof struct {
	SnapshotFound     bool   `json:"snapshot_found"`
	SnapshotSequence  uint64 `json:"snapshot_sequence"`
	SnapshotHash      string `json:"snapshot_hash"`
	BeforeSequence    uint64 `json:"before_sequence"`
	BeforeStateHash   string `json:"before_state_hash"`
	AfterSequence     uint64 `json:"after_sequence"`
	AfterStateHash    string `json:"after_state_hash"`
	RecordCountBefore uint64 `json:"record_count_before"`
	RecordCountAfter  uint64 `json:"record_count_after"`
	Unchanged         bool   `json:"unchanged"`
}

func (f *runnerFacade) capture(ctx context.Context, runner *tradingruntime.MarketRunner, account domain.AccountID, orderID domain.OrderID) (restartCapture, error) {
	status := runner.Status()
	order, found, err := runner.Order(orderID)
	if err != nil {
		return restartCapture{}, err
	}
	trades, err := runner.Trades(account)
	if err != nil {
		return restartCapture{}, err
	}
	balances, err := runner.Balances(account)
	if err != nil {
		return restartCapture{}, err
	}
	records, err := f.store.RecordsAfter(ctx, 0)
	if err != nil {
		return restartCapture{}, err
	}
	data, err := json.Marshal(records)
	if err != nil {
		return restartCapture{}, err
	}
	hash := sha256.Sum256(data)
	return restartCapture{Sequence: status.Sequence, StateHash: status.StateHash, Order: order,
		OrderFound: found, Trades: trades, Balances: balances, RecordHash: hex.EncodeToString(hash[:])}, nil
}

func (f *runnerFacade) Restart(ctx context.Context, account domain.AccountID, orderID domain.OrderID) (RestartProof, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	beforeCount := f.store.RecordCount()
	before, err := f.capture(ctx, f.runner, account, orderID)
	if err != nil {
		return RestartProof{}, err
	}
	if err := f.runner.Close(ctx); err != nil {
		return RestartProof{}, fmt.Errorf("close old runner: %w", err)
	}
	snapshot, found, err := f.store.Load(ctx)
	if err != nil {
		return RestartProof{}, fmt.Errorf("load persisted snapshot: %w", err)
	}
	proof := RestartProof{SnapshotFound: found, SnapshotSequence: snapshot.Sequence, SnapshotHash: snapshot.StateHash,
		BeforeSequence: before.Sequence, BeforeStateHash: before.StateHash, RecordCountBefore: beforeCount}
	if !found || snapshot.Sequence != before.Sequence || snapshot.StateHash != before.StateHash {
		return proof, fmt.Errorf("persisted snapshot does not bind the pre-restart state")
	}
	restored, err := tradingruntime.NewMarketRunner(ctx, f.market, f.store, f.store, f.config)
	if err != nil {
		return proof, fmt.Errorf("restore new runner: %w", err)
	}
	after, err := f.capture(ctx, restored, account, orderID)
	if err != nil {
		_ = restored.Close(context.Background())
		return proof, err
	}
	proof.AfterSequence = after.Sequence
	proof.AfterStateHash = after.StateHash
	proof.RecordCountAfter = f.store.RecordCount()
	proof.Unchanged = reflect.DeepEqual(before, after) && proof.RecordCountBefore == proof.RecordCountAfter &&
		proof.SnapshotFound && proof.SnapshotSequence == proof.AfterSequence && proof.SnapshotHash == proof.AfterStateHash
	if !proof.Unchanged {
		_ = restored.Close(context.Background())
		return proof, fmt.Errorf("restored runner differs from persisted pre-restart state")
	}
	f.runner = restored
	return proof, nil
}

func (f *runnerFacade) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runner.Close(ctx)
}
