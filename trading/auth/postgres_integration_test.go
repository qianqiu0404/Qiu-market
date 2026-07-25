package auth_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/auth"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func TestPostgresSessionHashesTokensAndEnforcesAllowedLogin(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgresstore.EnsureSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.NewPostgresSessionStore(pool, "qianqiu0404")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Create(ctx, auth.Principal{
		AccountID:   "github:intruder",
		GitHubLogin: "intruder",
	}, time.Hour); !errors.Is(err, auth.ErrLoginDenied) {
		t.Fatalf("denied login error = %v", err)
	}
	credentials, err := sessions.Create(ctx, auth.Principal{
		AccountID:   "github:qianqiu0404",
		GitHubLogin: "qianqiu0404",
		Admin:       true,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sessions.Delete(context.Background(), credentials.SessionToken)
	}()
	session, found, err := sessions.Lookup(ctx, credentials.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if !found || session.Principal.AccountID != "github:qianqiu0404" ||
		!session.Principal.Admin || !auth.ValidateCSRF(session, credentials.CSRFToken) {
		t.Fatalf("persisted session = %+v found=%t", session, found)
	}
	if _, found, err := sessions.Lookup(ctx, credentials.CSRFToken); err != nil || found {
		t.Fatalf("CSRF token authenticated as session: found=%t err=%v", found, err)
	}
	if err := sessions.Delete(ctx, credentials.SessionToken); err != nil {
		t.Fatal(err)
	}
	if _, found, err := sessions.Lookup(ctx, credentials.SessionToken); err != nil || found {
		t.Fatalf("deleted session lookup: found=%t err=%v", found, err)
	}
}
