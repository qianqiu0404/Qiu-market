package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresSessionStore struct {
	pool         *pgxpool.Pool
	allowedLogin string
	now          func() time.Time
}

func NewPostgresSessionStore(
	pool *pgxpool.Pool,
	allowedLogin string,
) (*PostgresSessionStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pool is required")
	}
	if allowedLogin == "" {
		return nil, fmt.Errorf("allowed GitHub login is required")
	}
	return &PostgresSessionStore{
		pool:         pool,
		allowedLogin: allowedLogin,
		now:          time.Now,
	}, nil
}

func (s *PostgresSessionStore) Create(
	ctx context.Context,
	principal Principal,
	ttl time.Duration,
) (Credentials, error) {
	if principal.AccountID == "" || principal.GitHubLogin != s.allowedLogin || ttl <= 0 {
		if principal.GitHubLogin != s.allowedLogin {
			return Credentials{}, ErrLoginDenied
		}
		return Credentials{}, ErrInvalidSession
	}
	sessionToken, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}
	csrfToken, err := NewToken()
	if err != nil {
		return Credentials{}, err
	}
	sessionHash := sha256.Sum256([]byte(sessionToken))
	csrfHash := sha256.Sum256([]byte(csrfToken))
	expiresAt := s.now().UTC().Add(ttl)

	_, err = s.pool.Exec(ctx, `
		INSERT INTO trading_user_session (
			session_hash, account_id, github_login, csrf_hash, expires_at
		)
		VALUES ($1,$2,$3,$4,$5)
	`, sessionHash[:], principal.AccountID, principal.GitHubLogin, csrfHash[:], expiresAt)
	if err != nil {
		return Credentials{}, fmt.Errorf("create trading session: %w", err)
	}
	return Credentials{
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func (s *PostgresSessionStore) Lookup(
	ctx context.Context,
	token string,
) (Session, bool, error) {
	sessionHash, err := HashToken(token)
	if err != nil {
		return Session{}, false, nil
	}
	var (
		session  Session
		csrfHash []byte
	)
	err = s.pool.QueryRow(ctx, `
		SELECT account_id, github_login, csrf_hash, expires_at
		FROM trading_user_session
		WHERE session_hash=$1 AND expires_at>$2
	`, sessionHash[:], s.now().UTC()).Scan(
		&session.Principal.AccountID,
		&session.Principal.GitHubLogin,
		&csrfHash,
		&session.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("lookup trading session: %w", err)
	}
	if session.Principal.GitHubLogin != s.allowedLogin || len(csrfHash) != sha256.Size {
		return Session{}, false, fmt.Errorf("persisted trading session is invalid")
	}
	copy(session.CSRFHash[:], csrfHash)
	session.Principal.Admin = true
	return session, true, nil
}

func (s *PostgresSessionStore) Delete(ctx context.Context, token string) error {
	sessionHash, err := HashToken(token)
	if err != nil {
		return nil
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM trading_user_session WHERE session_hash=$1`,
		sessionHash[:],
	); err != nil {
		return fmt.Errorf("delete trading session: %w", err)
	}
	return nil
}

func (s *PostgresSessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM trading_user_session WHERE expires_at<=$1`,
		s.now().UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("delete expired trading sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
