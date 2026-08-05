package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

type oauthState struct {
	verifier  string
	expiresAt time.Time
}

type OAuthStateManager struct {
	mu     sync.Mutex
	ttl    time.Duration
	now    func() time.Time
	states map[[sha256.Size]byte]oauthState
}

func NewOAuthStateManager(ttl time.Duration) (*OAuthStateManager, error) {
	if ttl <= 0 {
		return nil, ErrInvalidSession
	}
	return &OAuthStateManager{
		ttl:    ttl,
		now:    time.Now,
		states: make(map[[sha256.Size]byte]oauthState),
	}, nil
}

func (m *OAuthStateManager) Issue() (state string, codeChallenge string, err error) {
	state, err = NewToken()
	if err != nil {
		return "", "", err
	}
	verifier, err := NewToken()
	if err != nil {
		return "", "", err
	}
	stateHash := sha256.Sum256([]byte(state))
	challengeHash := sha256.Sum256([]byte(verifier))
	codeChallenge = base64.RawURLEncoding.EncodeToString(challengeHash[:])

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	m.states[stateHash] = oauthState{
		verifier:  verifier,
		expiresAt: m.now().UTC().Add(m.ttl),
	}
	return state, codeChallenge, nil
}

func (m *OAuthStateManager) Consume(state string) (string, bool) {
	hash, err := HashToken(state)
	if err != nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
	value, exists := m.states[hash]
	if !exists {
		return "", false
	}
	delete(m.states, hash)
	return value.verifier, true
}

func (m *OAuthStateManager) deleteExpiredLocked() {
	now := m.now().UTC()
	for hash, value := range m.states {
		if !value.expiresAt.After(now) {
			delete(m.states, hash)
		}
	}
}
