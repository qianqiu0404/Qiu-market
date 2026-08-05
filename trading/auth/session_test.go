package auth_test

import (
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/auth"
)

func TestTokensCSRFAndOneTimeTickets(t *testing.T) {
	t.Parallel()

	sessionToken, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := auth.HashToken(sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := auth.HashToken(sessionToken)
	if err != nil || firstHash != secondHash {
		t.Fatalf("token hash mismatch: %x %x %v", firstHash, secondHash, err)
	}
	csrfToken, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	csrfHash, err := auth.HashToken(csrfToken)
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Session{CSRFHash: csrfHash}
	if !auth.ValidateCSRF(session, csrfToken) || auth.ValidateCSRF(session, sessionToken) {
		t.Fatal("CSRF validation did not compare the persisted hash")
	}

	tickets, err := auth.NewTicketManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{
		AccountID:   "github:qianqiu0404",
		GitHubLogin: "qianqiu0404",
		Admin:       true,
	}
	ticket, expiresAt, err := tickets.Issue(principal)
	if err != nil || time.Until(expiresAt) <= 0 {
		t.Fatalf("issue ticket = %q %s %v", ticket, expiresAt, err)
	}
	actual, ok := tickets.Consume(ticket)
	if !ok || actual != principal {
		t.Fatalf("consume ticket = %+v, %t", actual, ok)
	}
	if _, ok := tickets.Consume(ticket); ok {
		t.Fatal("ticket was accepted more than once")
	}

	states, err := auth.NewOAuthStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state, challenge, err := states.Issue()
	if err != nil || state == "" || challenge == "" {
		t.Fatalf("issue OAuth state = %q %q %v", state, challenge, err)
	}
	verifier, ok := states.Consume(state)
	if !ok || verifier == "" {
		t.Fatalf("consume OAuth state = %q, %t", verifier, ok)
	}
	if _, ok := states.Consume(state); ok {
		t.Fatal("OAuth state was accepted more than once")
	}
}
