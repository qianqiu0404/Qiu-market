package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema is embedded only for isolated trading tests and the standalone lab.
// Integrated S78 processes require the current migrations, including
// migrations/2026082800030.sql, to have run.
//
//go:embed schema.sql
var Schema string

var requiredTables = []string{
	"trading_market",
	"trading_event_batch",
	"trading_snapshot",
	"trading_outbox",
	"trading_event_feed",
	"trading_outbox_checkpoint",
	"trading_order",
	"trading_order_event",
	"trading_order_event_checkpoint",
	"trading_trade",
	"trading_balance",
	"trading_ledger_entry",
	"trading_projection_checkpoint",
	"trading_user_session",
}

// VerifySchema is fail-closed for the integrated service: runtime code cannot
// silently mutate the shared S78 schema or bypass the migration ledger.
func VerifySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("postgres pool is required")
	}
	for _, table := range requiredTables {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('public.' || $1) IS NOT NULL`,
			table,
		).Scan(&exists); err != nil {
			return fmt.Errorf("verify trading schema table %s: %w", table, err)
		}
		if !exists {
			return fmt.Errorf(
				"trading schema is not migrated: missing %s; run market-services migrate",
				table,
			)
		}
	}
	return nil
}
