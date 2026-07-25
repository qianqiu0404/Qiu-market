package exchange

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/orderbook"
	"github.com/the-web3/s78-market-services/trading/store"
)

var (
	ErrMissingStore       = errors.New("event and snapshot stores are required")
	ErrRecoveryDiverged   = errors.New("recovery diverged from persisted result")
	ErrOrderNotFound      = errors.New("order not found")
	ErrOrderNotOpen       = errors.New("order is not open")
	ErrOrderOwnerMismatch = errors.New("order belongs to another account")
)

type Exchange struct {
	mu        sync.Mutex
	eventLog  store.EventStore
	snapshots store.SnapshotStore
	state     *state
}

type BalanceView struct {
	Available int64 `json:"available"`
	Held      int64 `json:"held"`
}

func New(market domain.Market, eventLog store.EventStore, snapshots store.SnapshotStore) (*Exchange, error) {
	if eventLog == nil || snapshots == nil {
		return nil, ErrMissingStore
	}
	initial, err := newState(market)
	if err != nil {
		return nil, err
	}
	return &Exchange{
		eventLog:  eventLog,
		snapshots: snapshots,
		state:     initial,
	}, nil
}

func Restore(ctx context.Context, market domain.Market, eventLog store.EventStore, snapshots store.SnapshotStore) (*Exchange, error) {
	if eventLog == nil || snapshots == nil {
		return nil, ErrMissingStore
	}
	current, err := newState(market)
	if err != nil {
		return nil, err
	}
	snapshot, found, err := snapshots.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	if found {
		current, err = unmarshalState(snapshot.Payload)
		if err != nil {
			return nil, err
		}
		if current.market != market {
			return nil, fmt.Errorf("%w: snapshot market configuration differs", ErrRecoveryDiverged)
		}
		if current.sequence != snapshot.Sequence {
			return nil, fmt.Errorf("%w: snapshot sequence payload=%d metadata=%d",
				ErrRecoveryDiverged, current.sequence, snapshot.Sequence)
		}
		hash, hashErr := current.hash()
		if hashErr != nil {
			return nil, hashErr
		}
		if hash != snapshot.StateHash {
			return nil, fmt.Errorf("%w: snapshot hash have=%s want=%s",
				ErrRecoveryDiverged, hash, snapshot.StateHash)
		}
	}

	records, err := eventLog.RecordsAfter(ctx, current.sequence)
	if err != nil {
		return nil, fmt.Errorf("load event records: %w", err)
	}
	for _, record := range records {
		if record.Command.Sequence != current.sequence+1 {
			return nil, fmt.Errorf("%w: event sequence have=%d want=%d",
				ErrRecoveryDiverged, record.Command.Sequence, current.sequence+1)
		}
		if record.Command.RequestID == "" || record.Command.Fingerprint == "" {
			return nil, fmt.Errorf("%w: replayed command lacks idempotency identity", ErrRecoveryDiverged)
		}
		if _, duplicate := current.requests[record.Command.RequestID]; duplicate {
			return nil, fmt.Errorf("%w: replayed request %s is duplicated",
				ErrRecoveryDiverged, record.Command.RequestID)
		}

		trial, cloneErr := current.clone()
		if cloneErr != nil {
			return nil, cloneErr
		}
		trial.sequence = record.Command.Sequence
		result, applyErr := trial.apply(record.Command)
		if applyErr != nil {
			return nil, fmt.Errorf("%w: replay sequence %d: %v",
				ErrRecoveryDiverged, record.Command.Sequence, applyErr)
		}
		trial.requests[record.Command.RequestID] = requestRecord{
			Fingerprint: record.Command.Fingerprint,
			Result:      cloneResult(result),
		}
		if err := trial.validate(); err != nil {
			return nil, fmt.Errorf("%w: replay sequence %d validation: %v",
				ErrRecoveryDiverged, record.Command.Sequence, err)
		}
		hash, hashErr := trial.hash()
		if hashErr != nil {
			return nil, hashErr
		}
		if !reflect.DeepEqual(result, record.Result) || hash != record.StateHash {
			return nil, fmt.Errorf("%w: sequence %d result_or_hash_mismatch",
				ErrRecoveryDiverged, record.Command.Sequence)
		}
		current = trial
	}

	return &Exchange{
		eventLog:  eventLog,
		snapshots: snapshots,
		state:     current,
	}, nil
}

func (e *Exchange) Fund(ctx context.Context, request domain.FundRequest) (domain.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := request.Validate(e.state.market); err != nil {
		return domain.Result{}, err
	}
	fingerprint, err := domain.Fingerprint(request)
	if err != nil {
		return domain.Result{}, err
	}
	requestCopy := request
	return e.runLocked(ctx, domain.Command{
		RequestID:   request.RequestID,
		Fingerprint: fingerprint,
		Kind:        domain.CommandKindFund,
		Fund:        &requestCopy,
	})
}

func (e *Exchange) Submit(ctx context.Context, request domain.NewOrder) (domain.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := request.Validate(e.state.market); err != nil {
		return domain.Result{}, err
	}
	fingerprint, err := domain.Fingerprint(request)
	if err != nil {
		return domain.Result{}, err
	}
	requestCopy := request
	return e.runLocked(ctx, domain.Command{
		RequestID:   request.ClientOrderID,
		Fingerprint: fingerprint,
		Kind:        domain.CommandKindSubmitOrder,
		Submit:      &requestCopy,
	})
}

func (e *Exchange) Cancel(ctx context.Context, request domain.CancelOrder) (domain.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := request.Validate(); err != nil {
		return domain.Result{}, err
	}
	fingerprint, err := domain.Fingerprint(request)
	if err != nil {
		return domain.Result{}, err
	}
	requestCopy := request
	return e.runLocked(ctx, domain.Command{
		RequestID:   request.RequestID,
		Fingerprint: fingerprint,
		Kind:        domain.CommandKindCancelOrder,
		Cancel:      &requestCopy,
	})
}

func (e *Exchange) runLocked(ctx context.Context, command domain.Command) (domain.Result, error) {
	if err := ctx.Err(); err != nil {
		return domain.Result{}, err
	}
	if previous, exists := e.state.requests[command.RequestID]; exists {
		if previous.Fingerprint != command.Fingerprint {
			return domain.Result{}, domain.ErrIdempotencyConflict
		}
		return cloneResult(previous.Result), nil
	}

	command.Sequence = e.state.sequence + 1
	trial, err := e.state.clone()
	if err != nil {
		return domain.Result{}, err
	}
	trial.sequence = command.Sequence
	result, err := trial.apply(command)
	if err != nil {
		return domain.Result{}, err
	}
	trial.requests[command.RequestID] = requestRecord{
		Fingerprint: command.Fingerprint,
		Result:      cloneResult(result),
	}
	if err := trial.validate(); err != nil {
		return domain.Result{}, fmt.Errorf("validate trial state: %w", err)
	}
	hash, err := trial.hash()
	if err != nil {
		return domain.Result{}, err
	}
	record := store.Record{
		Command:   command,
		Result:    cloneResult(result),
		StateHash: hash,
	}
	if err := e.eventLog.Append(ctx, e.state.sequence, record); err != nil {
		return domain.Result{}, fmt.Errorf("append event batch: %w", err)
	}
	e.state = trial
	return cloneResult(result), nil
}

func (s *state) apply(command domain.Command) (domain.Result, error) {
	switch command.Kind {
	case domain.CommandKindFund:
		return s.applyFund(command)
	case domain.CommandKindSubmitOrder:
		return s.applySubmit(command)
	case domain.CommandKindCancelOrder:
		return s.applyCancel(command)
	default:
		return domain.Result{}, fmt.Errorf("unsupported command kind %d", command.Kind)
	}
}

func (s *state) applyFund(command domain.Command) (domain.Result, error) {
	if command.Fund == nil {
		return domain.Result{}, fmt.Errorf("fund command payload is required")
	}
	request := *command.Fund
	if err := request.Validate(s.market); err != nil {
		return domain.Result{}, err
	}
	if err := s.ledger.FundVirtual(
		fmt.Sprintf("fund:%020d", command.Sequence),
		"virtual-funding:"+request.RequestID,
		request.AccountID,
		request.Asset,
		request.Amount,
	); err != nil {
		return domain.Result{}, err
	}
	event := domain.Event{
		Sequence:  command.Sequence,
		Index:     1,
		Type:      domain.EventAccountFunded,
		AccountID: request.AccountID,
		Asset:     request.Asset,
		Amount:    request.Amount,
	}
	return domain.Result{Sequence: command.Sequence, Events: []domain.Event{event}}, nil
}

func (e *Exchange) SaveSnapshot(ctx context.Context) (store.Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	payload, err := e.state.marshal()
	if err != nil {
		return store.Snapshot{}, err
	}
	hash, err := e.state.hash()
	if err != nil {
		return store.Snapshot{}, err
	}
	snapshot := store.Snapshot{
		Sequence:  e.state.sequence,
		StateHash: hash,
		Payload:   payload,
	}
	if err := e.snapshots.Save(ctx, snapshot); err != nil {
		return store.Snapshot{}, fmt.Errorf("save snapshot: %w", err)
	}
	return snapshot, nil
}

func (e *Exchange) Sequence() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.sequence
}

func (e *Exchange) StateHash() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.hash()
}

func (e *Exchange) Balance(accountID domain.AccountID, asset domain.Asset) BalanceView {
	e.mu.Lock()
	defer e.mu.Unlock()
	available, held := e.state.ledger.UserBalance(accountID, asset)
	return BalanceView{Available: available, Held: held}
}

func (e *Exchange) PlatformFees(asset domain.Asset) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.ledger.Balance(ledger.PlatformFee(asset), asset)
}

func (e *Exchange) Order(orderID domain.OrderID) (domain.Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	order, exists := e.state.orders[orderID]
	return order, exists
}

func (e *Exchange) Book() orderbook.Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.book.Snapshot()
}

func (e *Exchange) Journal() []ledger.Transaction {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.ledger.Journal()
}

func (e *Exchange) Validate() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.validate()
}

func appendEvent(events []domain.Event, event domain.Event) []domain.Event {
	event.Index = uint32(len(events) + 1)
	return append(events, event)
}
