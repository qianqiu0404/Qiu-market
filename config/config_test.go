package config

import (
	"net/url"
	"testing"
)

func TestDBConfigPostgresURLEscapesCredentials(t *testing.T) {
	t.Parallel()
	value := DBConfig{
		Host:     "127.0.0.1",
		Port:     5432,
		Name:     "s78 market",
		User:     "test user",
		Password: "p@ss word:/?",
	}.PostgresURL()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "127.0.0.1:5432" || parsed.Path != "/s78 market" {
		t.Fatalf("unexpected PostgreSQL URL: %s", value)
	}
	if parsed.User.Username() != "test user" {
		t.Fatalf("username = %q", parsed.User.Username())
	}
	password, ok := parsed.User.Password()
	if !ok || password != "p@ss word:/?" {
		t.Fatalf("password did not round-trip")
	}
	if parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("sslmode missing: %s", value)
	}
}
