package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidSession = errors.New("invalid trading session")
	ErrLoginDenied    = errors.New("GitHub login is not allowed")
)

const tokenBytes = 32

type Principal struct {
	AccountID   string `json:"account_id"`
	GitHubLogin string `json:"github_login"`
	Admin       bool   `json:"admin"`
}

type Session struct {
	Principal Principal
	CSRFHash  [sha256.Size]byte
	ExpiresAt time.Time
}

type Credentials struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

type SessionStore interface {
	Create(context.Context, Principal, time.Duration) (Credentials, error)
	Lookup(context.Context, string) (Session, bool, error)
	Delete(context.Context, string) error
}

func ValidateCSRF(session Session, token string) bool {
	if token == "" {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], session.CSRFHash[:]) == 1
}

func NewToken() (string, error) {
	data := make([]byte, tokenBytes)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func HashToken(token string) ([sha256.Size]byte, error) {
	if token == "" {
		return [sha256.Size]byte{}, ErrInvalidSession
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != tokenBytes {
		return [sha256.Size]byte{}, ErrInvalidSession
	}
	return sha256.Sum256([]byte(token)), nil
}
