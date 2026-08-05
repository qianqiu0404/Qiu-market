package recovery

import (
	"context"
	"sync"

	"github.com/the-web3/s78-market-services/trading/domain"
)

type MemoryStore struct {
	mu      sync.RWMutex
	current map[domain.MarketID]Status
	history map[domain.MarketID][]Status
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		current: make(map[domain.MarketID]Status),
		history: make(map[domain.MarketID][]Status),
	}
}

func (s *MemoryStore) Load(
	_ context.Context,
	marketID domain.MarketID,
) (Status, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, found := s.current[marketID]
	return current, found, nil
}

func (s *MemoryStore) Save(
	_ context.Context,
	expectedVersion uint64,
	next Status,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found := s.current[next.MarketID]
	if (!found && expectedVersion != 0) || (found && current.Version != expectedVersion) {
		return ErrVersionConflict
	}
	if next.Version != expectedVersion+1 {
		return ErrVersionConflict
	}
	s.current[next.MarketID] = next
	s.history[next.MarketID] = append(s.history[next.MarketID], next)
	return nil
}

func (s *MemoryStore) History(marketID domain.MarketID) []Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Status(nil), s.history[marketID]...)
}
