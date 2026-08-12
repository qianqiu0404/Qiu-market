package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPracticePostgresBoundaryUsesServerIdentityOwnerAndReadOnlySession(t *testing.T) {
	stateDSN := os.Getenv("QIU_T1_TEST_STATE_DSN")
	referenceDSN := os.Getenv("QIU_T1_TEST_REFERENCE_DSN")
	if stateDSN == "" || referenceDSN == "" {
		t.Skip("QIU_T1_TEST_STATE_DSN and QIU_T1_TEST_REFERENCE_DSN are required")
	}
	ctx := context.Background()
	state, err := pgxpool.New(ctx, stateDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	referenceConfig, err := pgxpool.ParseConfig(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	referenceConfig.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	reference, err := pgxpool.NewWithConfig(ctx, referenceConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	if err := verifyPracticePostgresBoundary(ctx, state, reference); err != nil {
		t.Fatal(err)
	}
	if _, err := reference.Exec(ctx, "CREATE TABLE qiu_t1_must_not_write(value integer)"); err == nil {
		t.Fatal("reference session accepted DDL despite read-only boundary")
	}
	if _, err := reference.Exec(ctx, `
		INSERT INTO qiu_t1_reference_probe(value) VALUES (1)
	`); err == nil {
		t.Fatal("reference session accepted DML despite read-only boundary")
	}
	if _, err := reference.Exec(ctx, `SELECT value FROM qiu_t1_reference_probe`); err == nil {
		t.Fatal("reference role read a table outside its two-table allowlist")
	}

	separator := "?"
	if strings.Contains(stateDSN, "?") {
		separator = "&"
	}
	sameConfig, err := pgxpool.ParseConfig(stateDSN + separator + "application_name=different-string")
	if err != nil {
		t.Fatal(err)
	}
	sameConfig.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	same, err := pgxpool.NewWithConfig(ctx, sameConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer same.Close()
	if err := verifyPracticePostgresBoundary(ctx, state, same); err == nil {
		t.Fatal("same PostgreSQL database with a different DSN string was accepted")
	}

	if _, err := state.Exec(ctx, `UPDATE qiu_trading_practice_owner SET owner_key='drifted'`); err != nil {
		t.Fatal(err)
	}
	if err := verifyPracticePostgresBoundary(ctx, state, reference); err == nil {
		t.Fatal("drifted practice ownership marker was accepted")
	}
	if _, err := state.Exec(ctx, `
		UPDATE qiu_trading_practice_owner
		SET owner_key='qiu-market/trading-practice/v1'
		WHERE singleton=TRUE
	`); err != nil {
		t.Fatal(err)
	}
}
