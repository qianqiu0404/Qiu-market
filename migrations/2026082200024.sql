-- Bound K-line storage on a single-disk Mac mini production demo.
--
-- Large redundant indexes are removed by ops/macos/optimize-kline-indexes.sh
-- with CONCURRENTLY before this migration reaches production. The idempotent
-- DROP statements below keep clean installs and interrupted rollouts aligned.

BEGIN;

CREATE TABLE IF NOT EXISTS kline_retention_status (
    singleton       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    last_started_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    deleted_rows    JSONB NOT NULL DEFAULT '{}'::JSONB,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO kline_retention_status(singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

CREATE INDEX IF NOT EXISTS symbol_kline_interval_open_time_idx
    ON symbol_kline ("interval", open_time);

DROP INDEX IF EXISTS idx_symbol_kline_guid;
DROP INDEX IF EXISTS symbol_kline_market_interval_open_idx;
DROP INDEX IF EXISTS idx_symbol_kline_symbol_interval_created_at;
DROP INDEX IF EXISTS idx_symbol_kline_is_active;
DROP INDEX IF EXISTS idx_symbol_kline_created_at;

COMMIT;
