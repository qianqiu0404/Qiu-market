package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
)

func TestTradeV1MigrationEmptyLegacyAndDamagedProjection(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	oldMigration := mustReadMigration(t, "2026082100023.sql")
	v1Migration := mustReadMigration(t, "2026082800030.sql")

	t.Run("empty database is repeatable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pool := isolatedMigrationPool(t, ctx, dsn)
		mustExecMigration(t, ctx, pool, oldMigration)
		mustExecMigration(t, ctx, pool, v1Migration)
		mustExecMigration(t, ctx, pool, v1Migration)

		var lifecycleExists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('trading_order_event') IS NOT NULL`,
		).Scan(&lifecycleExists); err != nil {
			t.Fatal(err)
		}
		if !lifecycleExists {
			t.Fatal("Trade V1 lifecycle table was not created")
		}
	})

	t.Run("legacy order uses accepted event and batch times", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pool := isolatedMigrationPool(t, ctx, dsn)
		mustExecMigration(t, ctx, pool, oldMigration)
		acceptedAt := time.Date(2026, 8, 5, 1, 2, 3, 456_000, time.UTC)
		legacyOrder := domain.Order{
			ID:                "O-00000000000000000001",
			ClientOrderID:     "client-1",
			AccountID:         "alice",
			MarketID:          "BTC-USDT",
			Side:              domain.SideBuy,
			Type:              domain.OrderTypeLimit,
			TimeInForce:       domain.TimeInForceGTC,
			Price:             60_000_000_000,
			OriginalQuantity:  1_000,
			RemainingQuantity: 1_000,
			Status:            domain.OrderStatusOpen,
			AcceptedSequence:  1,
			LastSequence:      1,
		}
		legacyPayload, err := json.Marshal(legacyOrder)
		if err != nil {
			t.Fatal(err)
		}
		var payloadAcceptedSequence int64
		if err := pool.QueryRow(ctx,
			`SELECT ($1::jsonb->>'accepted_sequence')::bigint`, string(legacyPayload),
		).Scan(&payloadAcceptedSequence); err != nil {
			t.Fatal(err)
		}
		if payloadAcceptedSequence != 1 {
			t.Fatalf("current order payload accepted_sequence=%d", payloadAcceptedSequence)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO trading_market (
				market_id, base_asset, quote_asset, base_scale, quote_scale,
				price_tick, quantity_step, min_quantity, min_notional,
				maker_fee_bps, taker_fee_bps, configuration_epoch, current_sequence
			) VALUES ('BTC-USDT','BTC','USDT',100000000,1000000,
			          10000,100,1000,5000000,10,20,1,1)
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO trading_event_batch (
				market_id, sequence, schema_version, operation, account_id,
				request_id, fingerprint, command_payload, result_payload,
				journal_payload, projection_payload, state_hash, created_at
			) VALUES (
				'BTC-USDT',1,5,2,'alice','client-1','fingerprint',
				'{}','{"events":[]}','[]','{"orders":[],"trades":[],"balances":[]}',
				'rebuildable-hash',$1
			)
		`, acceptedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO trading_order (
				market_id, order_id, account_id, status, updated_sequence, payload
			) VALUES (
				'BTC-USDT','O-00000000000000000001','alice','open',1,$1::jsonb
			)
		`, string(legacyPayload)); err != nil {
			t.Fatal(err)
		}
		mustExecMigration(t, ctx, pool, v1Migration)

		var sequence int64
		var createdAt, updatedAt time.Time
		if err := pool.QueryRow(ctx, `
			SELECT accepted_sequence, created_at, updated_at
			FROM trading_order
			WHERE market_id='BTC-USDT' AND order_id='O-00000000000000000001'
		`).Scan(&sequence, &createdAt, &updatedAt); err != nil {
			t.Fatal(err)
		}
		if sequence != 1 || !createdAt.Equal(acceptedAt) || !updatedAt.Equal(acceptedAt) {
			t.Fatalf(
				"legacy backfill sequence/time=%d/%s/%s want 1/%s",
				sequence,
				createdAt,
				updatedAt,
				acceptedAt,
			)
		}
	})

	t.Run("damaged accepted sequence fails closed and rolls back", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pool := isolatedMigrationPool(t, ctx, dsn)
		mustExecMigration(t, ctx, pool, oldMigration)
		if _, err := pool.Exec(ctx, `
			INSERT INTO trading_market (
				market_id, base_asset, quote_asset, base_scale, quote_scale,
				price_tick, quantity_step, min_quantity, min_notional,
				maker_fee_bps, taker_fee_bps, configuration_epoch, current_sequence
			) VALUES ('BTC-USDT','BTC','USDT',100000000,1000000,
			          10000,100,1000,5000000,10,20,1,1);

			INSERT INTO trading_event_batch (
				market_id, sequence, schema_version, operation, account_id,
				request_id, fingerprint, command_payload, result_payload,
				journal_payload, projection_payload, state_hash
			) VALUES (
				'BTC-USDT',1,5,2,'alice','client-1','fingerprint',
				'{}','{"events":[]}','[]','{"orders":[],"trades":[],"balances":[]}',
				'rebuildable-hash'
			);

			INSERT INTO trading_order (
				market_id, order_id, account_id, status, updated_sequence, payload
			) VALUES ('BTC-USDT','broken','alice','open',1,'{}')
		`); err != nil {
			t.Fatal(err)
		}
		connection, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, migrationErr := connection.Exec(ctx, string(v1Migration))
		_, _ = connection.Exec(ctx, "ROLLBACK")
		connection.Release()
		if migrationErr == nil {
			t.Fatal("damaged order projection unexpectedly migrated")
		}

		var columnExists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_schema=current_schema()
				  AND table_name='trading_order'
				  AND column_name='accepted_sequence'
			)
		`).Scan(&columnExists); err != nil {
			t.Fatal(err)
		}
		if columnExists {
			t.Fatal("failed migration did not roll back its schema changes")
		}
	})
}

func mustReadMigration(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func isolatedMigrationPool(
	t *testing.T,
	ctx context.Context,
	dsn string,
) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("trade_v1_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupContext, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	})
	return pool
}

func mustExecMigration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	content []byte,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, string(content)); err != nil {
		t.Fatal(err)
	}
}
