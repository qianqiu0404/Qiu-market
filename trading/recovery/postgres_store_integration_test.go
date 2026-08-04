package recovery

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
)

func TestPostgresStoreAtomicallySwitchesToANewEpoch(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("qiu_recovery_%d", time.Now().UnixNano())
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`) }()

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE trading_market (market_id TEXT PRIMARY KEY);
		INSERT INTO trading_market VALUES ('BTC-USDT');
		CREATE TABLE trading_recovery_epoch (
			schema_version INTEGER NOT NULL,
			market_id TEXT NOT NULL REFERENCES trading_market(market_id),
			epoch_id TEXT NOT NULL,
			phase TEXT NOT NULL,
			runtime_sequence BIGINT NOT NULL,
			state_hash TEXT NOT NULL,
			ledger_balanced BOOLEAN NOT NULL,
			event_continuous BOOLEAN NOT NULL,
			projection_caught_up BOOLEAN NOT NULL,
			outbox_caught_up BOOLEAN NOT NULL,
			transport_healthy BOOLEAN NOT NULL,
			writes_enabled BOOLEAN NOT NULL,
			transport_sample_count INTEGER NOT NULL DEFAULT 0,
			transport_first_sample_at TIMESTAMPTZ,
			transport_last_sample_at TIMESTAMPTZ,
			transport_maximum_gap_ms BIGINT NOT NULL DEFAULT 0,
			transport_evidence_sha256 TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL,
			version BIGINT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (market_id, epoch_id)
		);
		CREATE TABLE trading_recovery_current (
			market_id TEXT PRIMARY KEY REFERENCES trading_market(market_id),
			epoch_id TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			FOREIGN KEY (market_id, epoch_id)
				REFERENCES trading_recovery_epoch(market_id, epoch_id)
		);
	`); err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(store, domain.MarketID("BTC-USDT"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Advance(ctx, PhaseDependenciesReady, Proof{}); err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.EpochID == first.EpochID || second.Phase != PhaseBootstrap {
		t.Fatalf("new epoch = %+v first=%+v", second, first)
	}
	loaded, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EpochID != second.EpochID || loaded.WritesEnabled {
		t.Fatalf("current epoch = %+v", loaded)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trading_recovery_epoch`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("epoch history count = %d", count)
	}
	if err := store.Save(ctx, loaded.Version-1, loaded); err == nil ||
		!strings.Contains(err.Error(), ErrVersionConflict.Error()) {
		t.Fatalf("stale CAS error = %v", err)
	}
}
