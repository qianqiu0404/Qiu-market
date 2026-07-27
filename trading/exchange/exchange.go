package exchange

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
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
	ErrPersistence        = errors.New("trading persistence failure")
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

type AssetBalanceView struct {
	Asset     domain.Asset `json:"asset"`
	Available int64        `json:"available"`
	Held      int64        `json:"held"`
}

type PriceLevelView struct {
	Price      int64 `json:"price"`
	Quantity   int64 `json:"quantity"`
	OrderCount int   `json:"order_count"`
}

type OrderBookView struct {
	MarketID domain.MarketID  `json:"market_id"`
	Sequence uint64           `json:"sequence"`
	Bids     []PriceLevelView `json:"bids"`
	Asks     []PriceLevelView `json:"asks"`
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
		if snapshot.SchemaVersion != store.CurrentSchemaVersion || snapshot.MarketID != market.ID {
			return nil, fmt.Errorf("%w: unsupported snapshot metadata", ErrRecoveryDiverged)
		}
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
		if record.SchemaVersion != store.CurrentSchemaVersion || record.MarketID != market.ID {
			return nil, fmt.Errorf("%w: unsupported event record metadata", ErrRecoveryDiverged)
		}
		if record.Command.Sequence != current.sequence+1 {
			return nil, fmt.Errorf("%w: event sequence have=%d want=%d",
				ErrRecoveryDiverged, record.Command.Sequence, current.sequence+1)
		}
		if record.Command.Fingerprint == "" ||
			record.Command.RequestKey.MarketID != market.ID ||
			validateCommandIdentity(record.Command) != nil {
			return nil, fmt.Errorf("%w: replayed command lacks idempotency identity", ErrRecoveryDiverged)
		}
		if err := record.Command.RequestKey.Validate(); err != nil {
			return nil, fmt.Errorf("%w: replayed command has invalid idempotency key", ErrRecoveryDiverged)
		}
		if _, duplicate := current.requests[record.Command.RequestKey]; duplicate {
			return nil, fmt.Errorf("%w: replayed request %s is duplicated",
				ErrRecoveryDiverged, record.Command.RequestKey.String())
		}

		trial, cloneErr := current.clone()
		if cloneErr != nil {
			return nil, cloneErr
		}
		trial.sequence = record.Command.Sequence
		journalStart := trial.ledger.JournalLen()
		result, applyErr := trial.apply(record.Command)
		if applyErr != nil {
			return nil, fmt.Errorf("%w: replay sequence %d: %v",
				ErrRecoveryDiverged, record.Command.Sequence, applyErr)
		}
		journalDelta, journalErr := trial.ledger.JournalFrom(journalStart)
		if journalErr != nil {
			return nil, journalErr
		}
		projection, projectionErr := trial.buildProjection(record.Command, result)
		if projectionErr != nil {
			return nil, projectionErr
		}
		trial.requests[record.Command.RequestKey] = requestRecord{
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
		if !reflect.DeepEqual(result, record.Result) ||
			!reflect.DeepEqual(journalDelta, record.Journal) ||
			!reflect.DeepEqual(projection, record.Projection) ||
			hash != record.StateHash {
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
		RequestID: request.RequestID,
		RequestKey: domain.NewIdempotencyKey(
			e.state.market.ID, request.AccountID, domain.CommandKindFund, request.RequestID,
		),
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
		RequestID: request.ClientOrderID,
		RequestKey: domain.NewIdempotencyKey(
			e.state.market.ID, request.AccountID, domain.CommandKindSubmitOrder, request.ClientOrderID,
		),
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
		RequestID: request.RequestID,
		RequestKey: domain.NewIdempotencyKey(
			e.state.market.ID, request.AccountID, domain.CommandKindCancelOrder, request.RequestID,
		),
		Fingerprint: fingerprint,
		Kind:        domain.CommandKindCancelOrder,
		Cancel:      &requestCopy,
	})
}

func (e *Exchange) runLocked(ctx context.Context, command domain.Command) (domain.Result, error) {
	if err := ctx.Err(); err != nil {
		return domain.Result{}, err
	}
	if err := validateCommandIdentity(command); err != nil ||
		command.RequestKey.MarketID != e.state.market.ID {
		return domain.Result{}, fmt.Errorf("%w: command idempotency key does not match command", domain.ErrInvalidRequest)
	}
	if previous, exists := e.state.requests[command.RequestKey]; exists {
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
	journalStart := trial.ledger.JournalLen()
	result, err := trial.apply(command)
	if err != nil {
		return domain.Result{}, err
	}
	journalDelta, err := trial.ledger.JournalFrom(journalStart)
	if err != nil {
		return domain.Result{}, err
	}
	projection, err := trial.buildProjection(command, result)
	if err != nil {
		return domain.Result{}, err
	}
	trial.requests[command.RequestKey] = requestRecord{
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
		SchemaVersion: store.CurrentSchemaVersion,
		MarketID:      e.state.market.ID,
		Command:       command,
		Result:        cloneResult(result),
		Journal:       journalDelta,
		Projection:    projection,
		StateHash:     hash,
	}
	if err := e.eventLog.Append(ctx, e.state.sequence, record); err != nil {
		return domain.Result{}, fmt.Errorf("%w: append event batch: %w", ErrPersistence, err)
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
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	snapshot := store.Snapshot{
		SchemaVersion: store.CurrentSchemaVersion,
		MarketID:      e.state.market.ID,
		Sequence:      e.state.sequence,
		StateHash:     hash,
		Payload:       payload,
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

func (e *Exchange) Market() domain.Market {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.market
}

func (e *Exchange) Balances(accountID domain.AccountID) []AssetBalanceView {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]AssetBalanceView, 0, 2)
	for _, asset := range []domain.Asset{e.state.market.BaseAsset, e.state.market.QuoteAsset} {
		available, held := e.state.ledger.UserBalance(accountID, asset)
		result = append(result, AssetBalanceView{Asset: asset, Available: available, Held: held})
	}
	return result
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

func (e *Exchange) Orders(accountID domain.AccountID, openOnly bool) []domain.Order {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]domain.Order, 0)
	for _, order := range e.state.orders {
		if accountID != "" && order.AccountID != accountID {
			continue
		}
		if openOnly && !order.IsOpen() {
			continue
		}
		result = append(result, order)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AcceptedSequence != result[j].AcceptedSequence {
			return result[i].AcceptedSequence > result[j].AcceptedSequence
		}
		return result[i].ID > result[j].ID
	})
	return result
}

func (e *Exchange) Trades(accountID domain.AccountID) []domain.Trade {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]domain.Trade, 0, len(e.state.trades))
	for i := len(e.state.trades) - 1; i >= 0; i-- {
		trade := e.state.trades[i]
		if accountID != "" && trade.BuyerAccountID != accountID && trade.SellerAccountID != accountID {
			continue
		}
		result = append(result, trade)
	}
	return result
}

func (e *Exchange) Book() orderbook.Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.book.Snapshot()
}

func (e *Exchange) Depth(levels int) (OrderBookView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if levels <= 0 {
		levels = 20
	}
	snapshot := e.state.book.Snapshot()
	bids, err := aggregateLevels(snapshot.Bids, levels)
	if err != nil {
		return OrderBookView{}, err
	}
	asks, err := aggregateLevels(snapshot.Asks, levels)
	if err != nil {
		return OrderBookView{}, err
	}
	return OrderBookView{
		MarketID: e.state.market.ID,
		Sequence: e.state.sequence,
		Bids:     bids,
		Asks:     asks,
	}, nil
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

func validateCommandIdentity(command domain.Command) error {
	if err := command.RequestKey.Validate(); err != nil {
		return err
	}
	if command.RequestID == "" || command.RequestKey.RequestID != command.RequestID ||
		command.RequestKey.Operation != command.Kind {
		return fmt.Errorf("request identity does not match command")
	}
	var accountID domain.AccountID
	switch command.Kind {
	case domain.CommandKindFund:
		if command.Fund == nil || command.Submit != nil || command.Cancel != nil ||
			command.Fund.RequestID != command.RequestID {
			return fmt.Errorf("fund payload does not match command")
		}
		accountID = command.Fund.AccountID
	case domain.CommandKindSubmitOrder:
		if command.Submit == nil || command.Fund != nil || command.Cancel != nil ||
			command.Submit.ClientOrderID != command.RequestID {
			return fmt.Errorf("submit payload does not match command")
		}
		accountID = command.Submit.AccountID
	case domain.CommandKindCancelOrder:
		if command.Cancel == nil || command.Fund != nil || command.Submit != nil ||
			command.Cancel.RequestID != command.RequestID {
			return fmt.Errorf("cancel payload does not match command")
		}
		accountID = command.Cancel.AccountID
	default:
		return fmt.Errorf("unsupported command kind")
	}
	if accountID != command.RequestKey.AccountID {
		return fmt.Errorf("request account does not match command payload")
	}
	return nil
}

func (s *state) buildProjection(command domain.Command, result domain.Result) (store.Projection, error) {
	orderIDs := make(map[domain.OrderID]struct{})
	accounts := map[domain.AccountID]struct{}{
		command.RequestKey.AccountID: {},
	}
	trades := make([]domain.Trade, 0)
	if result.OrderID != "" {
		orderIDs[result.OrderID] = struct{}{}
	}
	for _, event := range result.Events {
		if event.OrderID != "" {
			orderIDs[event.OrderID] = struct{}{}
		}
		if event.AccountID != "" {
			accounts[event.AccountID] = struct{}{}
		}
		if event.Trade != nil {
			trade := *event.Trade
			trades = append(trades, trade)
			orderIDs[trade.MakerOrderID] = struct{}{}
			orderIDs[trade.TakerOrderID] = struct{}{}
			accounts[trade.BuyerAccountID] = struct{}{}
			accounts[trade.SellerAccountID] = struct{}{}
		}
	}

	projection := store.Projection{
		Orders: make([]domain.Order, 0, len(orderIDs)),
		Trades: trades,
	}
	for orderID := range orderIDs {
		if order, exists := s.orders[orderID]; exists {
			projection.Orders = append(projection.Orders, order)
		}
	}
	sort.Slice(projection.Orders, func(i, j int) bool {
		return projection.Orders[i].ID < projection.Orders[j].ID
	})
	sort.Slice(projection.Trades, func(i, j int) bool {
		return projection.Trades[i].ID < projection.Trades[j].ID
	})

	accountIDs := make([]domain.AccountID, 0, len(accounts))
	for accountID := range accounts {
		if accountID != "" {
			accountIDs = append(accountIDs, accountID)
		}
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	for _, accountID := range accountIDs {
		for _, asset := range []domain.Asset{s.market.BaseAsset, s.market.QuoteAsset} {
			available, held := s.ledger.UserBalance(accountID, asset)
			if available < 0 || held < 0 {
				return store.Projection{}, fmt.Errorf("projection contains negative balance")
			}
			projection.Balances = append(projection.Balances, store.BalanceProjection{
				AccountID: accountID,
				Asset:     asset,
				Available: available,
				Held:      held,
			})
		}
	}
	return projection, nil
}

func aggregateLevels(orders []domain.Order, limit int) ([]PriceLevelView, error) {
	result := make([]PriceLevelView, 0, limit)
	for _, order := range orders {
		if len(result) == 0 || result[len(result)-1].Price != order.Price {
			if len(result) == limit {
				break
			}
			result = append(result, PriceLevelView{Price: order.Price})
		}
		level := &result[len(result)-1]
		quantity, err := domain.CheckedAdd(level.Quantity, order.RemainingQuantity)
		if err != nil {
			return nil, fmt.Errorf("aggregate price level %d: %w", order.Price, err)
		}
		level.Quantity = quantity
		level.OrderCount++
	}
	return result, nil
}
