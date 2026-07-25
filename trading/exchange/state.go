package exchange

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/orderbook"
)

type requestRecord struct {
	Fingerprint string        `json:"fingerprint"`
	Result      domain.Result `json:"result"`
}

type persistedRequest struct {
	RequestID   string        `json:"request_id"`
	Fingerprint string        `json:"fingerprint"`
	Result      domain.Result `json:"result"`
}

type persistedState struct {
	Market   domain.Market      `json:"market"`
	Sequence uint64             `json:"sequence"`
	Orders   []domain.Order     `json:"orders"`
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
	requests map[string]requestRecord
}

func newState(market domain.Market) (*state, error) {
	if err := market.Validate(); err != nil {
		return nil, err
	}
	book, err := orderbook.New(market.QuantityStep)
	if err != nil {
		return nil, err
	}
	return &state{
		market:   market,
		book:     book,
		ledger:   ledger.New(),
		orders:   make(map[domain.OrderID]domain.Order),
		requests: make(map[string]requestRecord),
	}, nil
}

func (s *state) clone() (*state, error) {
	return stateFromPersisted(s.persisted())
}

func (s *state) persisted() persistedState {
	persisted := persistedState{
		Market:   s.market,
		Sequence: s.sequence,
		Book:     s.book.Snapshot(),
		Ledger:   s.ledger.Snapshot(),
		Orders:   make([]domain.Order, 0, len(s.orders)),
		Requests: make([]persistedRequest, 0, len(s.requests)),
	}
	for _, order := range s.orders {
		persisted.Orders = append(persisted.Orders, order)
	}
	sort.Slice(persisted.Orders, func(i, j int) bool {
		return persisted.Orders[i].ID < persisted.Orders[j].ID
	})
	for requestID, record := range s.requests {
		persisted.Requests = append(persisted.Requests, persistedRequest{
			RequestID:   requestID,
			Fingerprint: record.Fingerprint,
			Result:      cloneResult(record.Result),
		})
	}
	sort.Slice(persisted.Requests, func(i, j int) bool {
		return persisted.Requests[i].RequestID < persisted.Requests[j].RequestID
	})
	return persisted
}

func stateFromPersisted(persisted persistedState) (*state, error) {
	if err := persisted.Market.Validate(); err != nil {
		return nil, fmt.Errorf("restore market: %w", err)
	}
	book, err := orderbook.FromSnapshot(persisted.Market.QuantityStep, persisted.Book)
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
		requests: make(map[string]requestRecord, len(persisted.Requests)),
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
		if request.RequestID == "" || request.Fingerprint == "" {
			return nil, fmt.Errorf("restore request: id and fingerprint are required")
		}
		if _, duplicate := restored.requests[request.RequestID]; duplicate {
			return nil, fmt.Errorf("restore request: duplicate request id %s", request.RequestID)
		}
		restored.requests[request.RequestID] = requestRecord{
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
