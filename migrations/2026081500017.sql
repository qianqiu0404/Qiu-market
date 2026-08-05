-- Stable per-provider asset universes and lossless venue availability.
--
-- A provider selection is versioned and switched by one state-row update.
-- Ticker failures update attempt/status metadata but preserve the last
-- successful values, so a transient upstream failure cannot silently change
-- page membership or erase the last known quote.

BEGIN;

CREATE TABLE IF NOT EXISTS provider_asset_selection_state (
    provider          TEXT        PRIMARY KEY,
    active_version    BIGINT      NOT NULL,
    target_count      INTEGER     NOT NULL,
    candidate_count   INTEGER     NOT NULL,
    selected_count    INTEGER     NOT NULL,
    generated_at      TIMESTAMPTZ NOT NULL,
    generation_reason TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (provider IN ('binance', 'coinbase', 'bybit', 'okx')),
    CHECK (active_version > 0),
    CHECK (target_count BETWEEN 1 AND 200),
    CHECK (candidate_count >= selected_count),
    CHECK (selected_count BETWEEN 0 AND target_count)
);

CREATE TABLE IF NOT EXISTS provider_asset_selection (
    provider          TEXT        NOT NULL,
    selection_version BIGINT      NOT NULL,
    asset_guid        TEXT        NOT NULL REFERENCES asset(guid),
    selection_rank    INTEGER     NOT NULL,
    market_cap_rank   INTEGER,
    selection_reason  TEXT        NOT NULL,
    selected_at       TIMESTAMPTZ NOT NULL,
    replaced_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, selection_version, asset_guid),
    UNIQUE (provider, selection_version, selection_rank),
    CHECK (provider IN ('binance', 'coinbase', 'bybit', 'okx')),
    CHECK (selection_version > 0),
    CHECK (selection_rank > 0),
    CHECK (market_cap_rank IS NULL OR market_cap_rank BETWEEN 1 AND 200)
);

CREATE INDEX IF NOT EXISTS provider_asset_selection_asset_idx
    ON provider_asset_selection(asset_guid, provider, selection_version);

ALTER TABLE asset_venue_snapshot
    ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS availability_status TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS last_error_class TEXT;

UPDATE asset_venue_snapshot
SET last_attempt_at = COALESCE(last_attempt_at, observed_at),
    last_success_at = CASE
        WHEN available THEN COALESCE(last_success_at, observed_at)
        ELSE last_success_at
    END,
    availability_status = CASE
        WHEN available THEN 'fresh'
        WHEN availability_status = 'unknown' THEN 'unavailable'
        ELSE availability_status
    END
WHERE last_attempt_at IS NULL
   OR (available AND last_success_at IS NULL)
   OR availability_status = 'unknown';

ALTER TABLE asset_venue_snapshot
    DROP CONSTRAINT IF EXISTS asset_venue_snapshot_availability_status_check;

ALTER TABLE asset_venue_snapshot
    ADD CONSTRAINT asset_venue_snapshot_availability_status_check
    CHECK (availability_status IN ('unknown', 'fresh', 'stale', 'unavailable'));

CREATE INDEX IF NOT EXISTS asset_venue_snapshot_freshness_idx
    ON asset_venue_snapshot(provider, price_kind, last_success_at DESC);

COMMIT;
