package auth

import (
	"crypto/sha256"
	"sync"
	"time"
)

type ticket struct {
	principal Principal
	expiresAt time.Time
}

type TicketManager struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	tickets map[[sha256.Size]byte]ticket
}

func NewTicketManager(ttl time.Duration) (*TicketManager, error) {
	if ttl <= 0 {
		return nil, ErrInvalidSession
	}
	return &TicketManager{
		ttl:     ttl,
		now:     time.Now,
		tickets: make(map[[sha256.Size]byte]ticket),
	}, nil
}

func (m *TicketManager) Issue(principal Principal) (string, time.Time, error) {
	if principal.AccountID == "" {
		return "", time.Time{}, ErrInvalidSession
	}
	token, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	hash := sha256.Sum256([]byte(token))
	expiresAt := m.now().UTC().Add(m.ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	m.tickets[hash] = ticket{principal: principal, expiresAt: expiresAt}
	return token, expiresAt, nil
}

func (m *TicketManager) Consume(token string) (Principal, bool) {
	hash, err := HashToken(token)
	if err != nil {
		return Principal{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	value, exists := m.tickets[hash]
	if !exists {
		return Principal{}, false
	}
	delete(m.tickets, hash)
	return value.principal, true
}

func (m *TicketManager) deleteExpiredLocked() {
	now := m.now().UTC()
	for hash, value := range m.tickets {
		if !value.expiresAt.After(now) {
			delete(m.tickets, hash)
		}
	}
}
