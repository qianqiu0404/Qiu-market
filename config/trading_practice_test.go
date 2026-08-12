package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTradingPostgresURLsRequireIndependentOwnerOnlyFiles(t *testing.T) {
	directory := t.TempDir()
	state := filepath.Join(directory, "state")
	reference := filepath.Join(directory, "reference")
	mustWritePrivate(t, state, "postgres://practice@127.0.0.1/trading_state?sslmode=disable\n")
	mustWritePrivate(t, reference, "postgres://readonly@127.0.0.1/market_reference?sslmode=disable\n")
	config := Config{Trading: TradingConfig{
		PracticeMode: true, StateDSNFile: state, ReferenceDSNFile: reference,
	}}
	stateURL, referenceURL, err := config.TradingPostgresURLs()
	if err != nil || !strings.Contains(stateURL, "trading_state") ||
		!strings.Contains(referenceURL, "market_reference") {
		t.Fatalf("urls=%q/%q err=%v", stateURL, referenceURL, err)
	}

	if err := os.Chmod(reference, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.TradingPostgresURLs(); err == nil || strings.Contains(err.Error(), "readonly") {
		t.Fatalf("unsafe file error leaked or was accepted: %v", err)
	}
	if err := os.Chmod(reference, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(reference); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, reference); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.TradingPostgresURLs(); err == nil {
		t.Fatal("symlink DSN file was accepted")
	}
}

func TestTradingPostgresURLsRejectSameDatabaseAndPreserveLegacy(t *testing.T) {
	directory := t.TempDir()
	state := filepath.Join(directory, "state")
	reference := filepath.Join(directory, "reference")
	value := "postgres://practice@127.0.0.1/shared?sslmode=disable\n"
	mustWritePrivate(t, state, value)
	mustWritePrivate(t, reference, value)
	config := Config{Trading: TradingConfig{
		PracticeMode: true, StateDSNFile: state, ReferenceDSNFile: reference,
	}}
	if _, _, err := config.TradingPostgresURLs(); err == nil {
		t.Fatal("shared practice database was accepted")
	}
	legacy := Config{MasterDB: DBConfig{Host: "127.0.0.1", Port: 5432, Name: "legacy"}}
	stateURL, referenceURL, err := legacy.TradingPostgresURLs()
	if err != nil || stateURL != referenceURL || !strings.Contains(stateURL, "/legacy") {
		t.Fatalf("legacy urls=%q/%q err=%v", stateURL, referenceURL, err)
	}
}

func mustWritePrivate(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
