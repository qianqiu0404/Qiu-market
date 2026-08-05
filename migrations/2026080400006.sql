-- Slice 3/4: provider health, durable K-line repair work, and provider-owned
-- catalog identifiers. Runtime switches remain guarded by shadow comparison.

BEGIN;

ALTER TABLE exchange_symbol
    ADD COLUMN IF NOT EXISTS source_symbol TEXT;

UPDATE exchange_symbol es
SET source_symbol = replace(upper(s.symbol_name), '/', '')
FROM exchange e, symbol s
WHERE es.exchange_guid = e.guid
  AND es.symbol_guid = s.guid
  AND e.code = 'binance'
  AND (es.source_symbol IS NULL OR btrim(es.source_symbol) = '');

CREATE UNIQUE INDEX IF NOT EXISTS exchange_symbol_provider_source_uidx
    ON exchange_symbol(exchange_guid, source_symbol)
    WHERE source_symbol IS NOT NULL;

CREATE TABLE IF NOT EXISTS asset_external_mapping (
    provider    TEXT        NOT NULL,
    asset_guid TEXT        NOT NULL REFERENCES asset(guid),
    external_id TEXT       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, asset_guid),
    UNIQUE (provider, external_id)
);

INSERT INTO asset_external_mapping(provider, asset_guid, external_id)
SELECT 'coingecko', guid,
       CASE upper(asset_symbol)
           WHEN 'BTC' THEN 'bitcoin'
           WHEN 'ETH' THEN 'ethereum'
           WHEN 'SOL' THEN 'solana'
           WHEN 'BNB' THEN 'binancecoin'
           WHEN 'XRP' THEN 'ripple'
           WHEN 'DOGE' THEN 'dogecoin'
       END
FROM asset
WHERE upper(asset_symbol) IN ('BTC', 'ETH', 'SOL', 'BNB', 'XRP', 'DOGE')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS market_provider_status (
    provider             TEXT        NOT NULL,
    source_key           TEXT        NOT NULL,
    last_attempt_at      TIMESTAMPTZ,
    last_success_at      TIMESTAMPTZ,
    consecutive_failures INTEGER     NOT NULL DEFAULT 0,
    last_source_time     TIMESTAMPTZ,
    last_error_class     TEXT,
    last_error_summary   TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, source_key),
    CHECK (consecutive_failures >= 0)
);

CREATE INDEX IF NOT EXISTS market_provider_status_updated_idx
    ON market_provider_status(provider, updated_at DESC);

CREATE TABLE IF NOT EXISTS kline_repair_task (
    task_key       TEXT        PRIMARY KEY,
    provider       TEXT        NOT NULL,
    market_id      TEXT        NOT NULL REFERENCES exchange_symbol(guid),
    source_symbol  TEXT        NOT NULL,
    interval       TEXT        NOT NULL,
    gap_start      TIMESTAMPTZ NOT NULL,
    gap_end        TIMESTAMPTZ NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'pending',
    attempt_count  INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    locked_at      TIMESTAMPTZ,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    CHECK (gap_end > gap_start),
    CHECK (attempt_count >= 0)
);

CREATE INDEX IF NOT EXISTS kline_repair_task_claim_idx
    ON kline_repair_task(provider, status, next_attempt_at, created_at);

COMMIT;
