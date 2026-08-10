package goldenpath

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/the-web3/s78-market-services/trading/auth"
)

type memorySessionStore struct {
	mu       sync.Mutex
	sessions map[[sha256.Size]byte]auth.Session
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{sessions: make(map[[sha256.Size]byte]auth.Session)}
}

func (s *memorySessionStore) Create(ctx context.Context, principal auth.Principal, ttl time.Duration) (auth.Credentials, error) {
	if err := ctx.Err(); err != nil {
		return auth.Credentials{}, err
	}
	sessionToken, err := auth.NewToken()
	if err != nil {
		return auth.Credentials{}, err
	}
	csrfToken, err := auth.NewToken()
	if err != nil {
		return auth.Credentials{}, err
	}
	sessionHash, err := auth.HashToken(sessionToken)
	if err != nil {
		return auth.Credentials{}, err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	s.mu.Lock()
	s.sessions[sessionHash] = auth.Session{
		Principal: principal,
		CSRFHash:  sha256.Sum256([]byte(csrfToken)),
		ExpiresAt: expiresAt,
	}
	s.mu.Unlock()
	return auth.Credentials{SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expiresAt}, nil
}

func (s *memorySessionStore) Lookup(ctx context.Context, token string) (auth.Session, bool, error) {
	if err := ctx.Err(); err != nil {
		return auth.Session{}, false, err
	}
	hash, err := auth.HashToken(token)
	if err != nil {
		return auth.Session{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[hash]
	if ok && !session.ExpiresAt.After(time.Now().UTC()) {
		delete(s.sessions, hash)
		return auth.Session{}, false, nil
	}
	return session, ok, nil
}

func (s *memorySessionStore) Delete(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash, err := auth.HashToken(token)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	delete(s.sessions, hash)
	s.mu.Unlock()
	return nil
}
