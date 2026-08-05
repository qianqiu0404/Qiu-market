package exchange

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/orderbook"
)

type requestRecord struct {
	Fingerprint string        `json:"fingerprint"`
	Result      domain.Result `json:"result"`
}

type persistedRequest struct {
	Key         domain.IdempotencyKey `json:"key"`
	Fingerprint string                `json:"fingerprint"`
	Result      domain.Result         `json:"result"`
}

type persistedState struct {
	Market   domain.Market      `json:"market"`
	Sequence uint64             `json:"sequence"`
	Orders   []domain.Order     `json:"orders"`
	Trades   []domain.Trade     `json:"trades"`
	Requests []persistedRequest `json:"requests"`
	Book     orderbook.Snapshot `json:"book"`
	Ledger   ledger.Snapshot    `json:"ledger"`
}

type state struct {
	market   domain.Market
	sequence uint64
	book     *orderbook.Book
	ledger   *ledger.Ledger
	orders   map[domain.OrderID]domain.Order
	trades   []domain.Trade
	requests map[domain.IdempotencyKey]requestRecord
}

const ephemeralDemoMakerAccount = domain.AccountID("system:demo-maker")

func newState(market domain.Market) (*state, error) {
	if err := market.Validate(); err != nil {
		return nil, err
	}
	book, err := orderbook.New(market)
	if err != nil {
		return nil, err
	}
	return &state{
		market:   market,
		book:     book,
		ledger:   ledger.New(),
		orders:   make(map[domain.OrderID]domain.Order),
		requests: make(map[domain.IdempotencyKey]requestRecord),
	}, nil
}

func (s *state) clone() (*state, error) {
	book, err := orderbook.FromSnapshot(s.market, s.book.Snapshot())
	if err != nil {
		return nil, fmt.Errorf("clone order book: %w", err)
	}
	cloned := &state{
		market:   s.market,
		sequence: s.sequence,
		book:     book,
		ledger:   s.ledger.Clone(),
		orders:   make(map[domain.OrderID]domain.Order, len(s.orders)),
		trades:   append([]domain.Trade(nil), s.trades...),
		requests: make(map[domain.IdempotencyKey]requestRecord, len(s.requests)),
	}
	for orderID, order := range s.orders {
		cloned.orders[orderID] = order
	}
	for key, record := range s.requests {
		cloned.requests[key] = requestRecord{
			Fingerprint: record.Fingerprint,
			Result:      cloneResult(record.Result),
		}
	}
	return cloned, nil
}

func (s *state) persisted() persistedState {
	persisted := persistedState{
		Market:   s.market,
		Sequence: s.sequence,
		Book:     s.book.Snapshot(),
		Ledger:   s.ledger.Snapshot(),
		Orders:   make([]domain.Order, 0, len(s.orders)),
		Trades:   append([]domain.Trade(nil), s.trades...),
		Requests: make([]persistedRequest, 0, len(s.requests)),
	}
	for _, order := range s.orders {
		persisted.Orders = append(persisted.Orders, order)
	}
	sort.Slice(persisted.Orders, func(i, j int) bool {
		return persisted.Orders[i].ID < persisted.Orders[j].ID
	})
	for key, record := range s.requests {
		persisted.Requests = append(persisted.Requests, persistedRequest{
			Key:         key,
			Fingerprint: record.Fingerprint,
			Result:      cloneResult(record.Result),
		})
	}
	sort.Slice(persisted.Requests, func(i, j int) bool {
		return persisted.Requests[i].Key.String() < persisted.Requests[j].Key.String()
	})
	return persisted
}

// compactEphemeralSystemHistory removes only closed demo-maker orders and its
// in-memory idempotency cache. PostgreSQL event batches and order projections
// remain immutable and complete; user order history, user idempotency, trades,
// balances, open quotes and the order book are preserved.
func (s *state) compactEphemeralSystemHistory() {
	for orderID, order := range s.orders {
		if order.AccountID == ephemeralDemoMakerAccount && !order.IsOpen() {
			delete(s.orders, orderID)
		}
	}
	for key := range s.requests {
		if key.AccountID == ephemeralDemoMakerAccount &&
			key.Operation != domain.CommandKindFund {
			delete(s.requests, key)
		}
	}
}

func (s *state) compactForSchema(schemaVersion uint16) error {
	if schemaVersion >= 4 {
		s.compactEphemeralSystemHistory()
	}
	if schemaVersion >= 5 {
		if err := s.ledger.Compact(); err != nil {
			return fmt.Errorf("compact runtime ledger: %w", err)
		}
	}
	return nil
}

func stateFromPersisted(persisted persistedState) (*state, error) {
	if err := persisted.Market.Validate(); err != nil {
		return nil, fmt.Errorf("restore market: %w", err)
	}
	book, err := orderbook.FromSnapshot(persisted.Market, persisted.Book)
	if err != nil {
		return nil, fmt.Errorf("restore order book: %w", err)
	}
	restoredLedger, err := ledger.FromSnapshot(persisted.Ledger)
	if err != nil {
		return nil, fmt.Errorf("restore ledger: %w", err)
	}
	restored := &state{
		market:   persisted.Market,
		sequence: persisted.Sequence,
		book:     book,
		ledger:   restoredLedger,
		orders:   make(map[domain.OrderID]domain.Order, len(persisted.Orders)),
		trades:   append([]domain.Trade(nil), persisted.Trades...),
		requests: make(map[domain.IdempotencyKey]requestRecord, len(persisted.Requests)),
	}
	for _, order := range persisted.Orders {
		if order.ID == "" {
			return nil, fmt.Errorf("restore order: empty order id")
		}
		if _, duplicate := restored.orders[order.ID]; duplicate {
			return nil, fmt.Errorf("restore order: duplicate order id %s", order.ID)
		}
		restored.orders[order.ID] = order
	}
	for _, request := range persisted.Requests {
		if err := request.Key.Validate(); err != nil || request.Fingerprint == "" {
			return nil, fmt.Errorf("restore request: valid key and fingerprint are required")
		}
		if _, duplicate := restored.requests[request.Key]; duplicate {
			return nil, fmt.Errorf("restore request: duplicate request key %s", request.Key.String())
		}
		restored.requests[request.Key] = requestRecord{
			Fingerprint: request.Fingerprint,
			Result:      cloneResult(request.Result),
		}
	}
	if err := restored.validate(); err != nil {
		return nil, err
	}
	return restored, nil
}

func (s *state) marshal() ([]byte, error) {
	data, err := json.Marshal(s.persisted())
	if err != nil {
		return nil, fmt.Errorf("marshal exchange state: %w", err)
	}
	return data, nil
}

func unmarshalState(data []byte) (*state, error) {
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("unmarshal exchange state: %w", err)
	}
	return stateFromPersisted(persisted)
}

func (s *state) hash() (string, error) {
	data, err := s.marshal()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *state) validate() error {
	if err := s.market.Validate(); err != nil {
		return err
	}
	if err := s.book.Validate(); err != nil {
		return fmt.Errorf("validate order book: %w", err)
	}
	if err := s.ledger.Validate(); err != nil {
		return fmt.Errorf("validate ledger: %w", err)
	}
	openInBook := make(map[domain.OrderID]domain.Order)
	bookSnapshot := s.book.Snapshot()
	for _, order := range append(bookSnapshot.Bids, bookSnapshot.Asks...) {
		openInBook[order.ID] = order
		stateOrder, exists := s.orders[order.ID]
		if !exists || stateOrder != order {
			return fmt.Errorf("order %s differs between order index and book", order.ID)
		}
	}
	for orderID, order := range s.orders {
		_, inBook := openInBook[orderID]
		if order.IsOpen() != inBook {
			return fmt.Errorf("order %s open=%t but in_book=%t", orderID, order.IsOpen(), inBook)
		}
		if order.RemainingQuantity < 0 || order.FilledQuantity < 0 || order.RemainingQuoteBudget < 0 || order.SpentQuote < 0 {
			return fmt.Errorf("order %s contains negative quantities", orderID)
		}
	}
	type heldKey struct {
		account domain.AccountID
		asset   domain.Asset
	}
	expectedHeld := make(map[heldKey]int64)
	for orderID, order := range s.orders {
		if order.IsOpen() {
			expectedAsset := s.market.BaseAsset
			expectedAmount := order.RemainingQuantity
			if order.Side == domain.SideBuy {
				expectedAsset = s.market.QuoteAsset
				var err error
				expectedAmount, err = s.market.QuoteAmountCeil(order.Price, order.RemainingQuantity)
				if err != nil {
					return fmt.Errorf("validate order %s hold: %w", orderID, err)
				}
			}
			if order.HeldAsset != expectedAsset || order.HeldAmount != expectedAmount {
				return fmt.Errorf("order %s hold asset/amount mismatch", orderID)
			}
			key := heldKey{account: order.AccountID, asset: order.HeldAsset}
			total, err := domain.CheckedAdd(expectedHeld[key], order.HeldAmount)
			if err != nil {
				return fmt.Errorf("sum held amount for order %s: %w", orderID, err)
			}
			expectedHeld[key] = total
		} else if order.HeldAmount != 0 || order.HeldAsset != "" {
			return fmt.Errorf("closed order %s retains held funds", orderID)
		}
	}
	for key, expected := range expectedHeld {
		if actual := s.ledger.Balance(ledger.UserHeld(key.account), key.asset); actual != expected {
			return fmt.Errorf("held balance mismatch for account %s asset %s: have=%d want=%d",
				key.account, key.asset, actual, expected)
		}
	}
	for _, balance := range s.ledger.Balances() {
		if !strings.HasPrefix(balance.Account, "user:") || !strings.HasSuffix(balance.Account, ":held") {
			continue
		}
		account := domain.AccountID(strings.TrimSuffix(strings.TrimPrefix(balance.Account, "user:"), ":held"))
		if expectedHeld[heldKey{account: account, asset: balance.Asset}] != balance.Amount {
			return fmt.Errorf("ledger contains unmatched held balance for account %s asset %s", account, balance.Asset)
		}
	}
	seenTrades := make(map[domain.TradeID]struct{}, len(s.trades))
	for _, trade := range s.trades {
		if trade.ID == "" || trade.MarketID != s.market.ID {
			return fmt.Errorf("trade identity is invalid")
		}
		if _, duplicate := seenTrades[trade.ID]; duplicate {
			return fmt.Errorf("duplicate trade %s", trade.ID)
		}
		seenTrades[trade.ID] = struct{}{}
	}
	for key, request := range s.requests {
		if err := key.Validate(); err != nil || key.MarketID != s.market.ID || request.Fingerprint == "" {
			return fmt.Errorf("invalid request record %s", key.String())
		}
	}
	return nil
}

func cloneResult(result domain.Result) domain.Result {
	cloned := result
	cloned.Events = append([]domain.Event(nil), result.Events...)
	for i := range cloned.Events {
		if result.Events[i].Trade != nil {
			trade := *result.Events[i].Trade
			cloned.Events[i].Trade = &trade
		}
	}
	return cloned
}
