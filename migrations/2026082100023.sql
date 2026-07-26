-- Virtual BTC/USDT spot bounded context.
--
-- Event batches are the source of truth. Orders, trades, balances, ledger
-- entries and checkpoints are rebuildable projections committed in the same
-- PostgreSQL transaction as the event batch and outbox.

BEGIN;

CREATE TABLE IF NOT EXISTS trading_market (
    market_id              TEXT PRIMARY KEY,
    base_asset             TEXT NOT NULL,
    quote_asset            TEXT NOT NULL,
    base_scale             BIGINT NOT NULL CHECK (base_scale > 0),
    quote_scale            BIGINT NOT NULL CHECK (quote_scale > 0),
    price_tick             BIGINT NOT NULL CHECK (price_tick > 0),
    quantity_step          BIGINT NOT NULL CHECK (quantity_step > 0),
    min_quantity           BIGINT NOT NULL CHECK (min_quantity > 0),
    min_notional           BIGINT NOT NULL CHECK (min_notional > 0),
    maker_fee_bps          BIGINT NOT NULL CHECK (maker_fee_bps >= 0 AND maker_fee_bps < 10000),
    taker_fee_bps          BIGINT NOT NULL CHECK (taker_fee_bps >= 0 AND taker_fee_bps < 10000),
    configuration_epoch    BIGINT NOT NULL CHECK (configuration_epoch > 0),
    current_sequence       BIGINT NOT NULL DEFAULT 0 CHECK (current_sequence >= 0),
    enabled                BOOLEAN NOT NULL DEFAULT TRUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS trading_event_batch (
    market_id          TEXT NOT NULL REFERENCES trading_market(market_id),
    sequence           BIGINT NOT NULL CHECK (sequence > 0),
    schema_version     SMALLINT NOT NULL CHECK (schema_version > 0),
    operation          SMALLINT NOT NULL CHECK (operation > 0),
    account_id         TEXT NOT NULL,
    request_id         TEXT NOT NULL,
    fingerprint        TEXT NOT NULL,
    command_payload    JSONB NOT NULL,
    result_payload     JSONB NOT NULL,
    journal_payload    JSONB NOT NULL,
    projection_payload JSONB NOT NULL,
    state_hash         TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (market_id, sequence),
    UNIQUE (market_id, account_id, operation, request_id)
);

ALTER TABLE trading_event_batch
    ADD COLUMN IF NOT EXISTS projection_payload JSONB NOT NULL
    DEFAULT '{"orders":[],"trades":[],"balances":[]}'::JSONB;
ALTER TABLE trading_event_batch
    ALTER COLUMN projection_payload DROP DEFAULT;

CREATE TABLE IF NOT EXISTS trading_snapshot (
    market_id       TEXT PRIMARY KEY REFERENCES trading_market(market_id),
    schema_version  SMALLINT NOT NULL CHECK (schema_version > 0),
    sequence        BIGINT NOT NULL CHECK (sequence >= 0),
    state_hash      TEXT NOT NULL,
    payload         BYTEA NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS trading_outbox (
    market_id       TEXT NOT NULL,
    sequence        BIGINT NOT NULL,
    event_index     INTEGER NOT NULL CHECK (event_index > 0),
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (market_id, sequence, event_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_outbox_unpublished_idx
    ON trading_outbox (market_id, sequence, event_index)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS trading_order (
    market_id        TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    order_id         TEXT NOT NULL,
    account_id       TEXT NOT NULL,
    status           TEXT NOT NULL,
    updated_sequence BIGINT NOT NULL CHECK (updated_sequence > 0),
    payload          JSONB NOT NULL,
    PRIMARY KEY (market_id, order_id),
    FOREIGN KEY (market_id, updated_sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_order_account_idx
    ON trading_order (market_id, account_id, updated_sequence DESC);

CREATE TABLE IF NOT EXISTS trading_trade (
    market_id         TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    trade_id          TEXT NOT NULL,
    sequence          BIGINT NOT NULL CHECK (sequence > 0),
    event_index       INTEGER NOT NULL CHECK (event_index > 0),
    buyer_account_id  TEXT NOT NULL,
    seller_account_id TEXT NOT NULL,
    payload           JSONB NOT NULL,
    PRIMARY KEY (market_id, trade_id),
    UNIQUE (market_id, sequence, event_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_balance (
    market_id        TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    account_id       TEXT NOT NULL,
    asset            TEXT NOT NULL,
    available        BIGINT NOT NULL CHECK (available >= 0),
    held             BIGINT NOT NULL CHECK (held >= 0),
    updated_sequence BIGINT NOT NULL CHECK (updated_sequence > 0),
    PRIMARY KEY (market_id, account_id, asset),
    FOREIGN KEY (market_id, updated_sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_ledger_entry (
    market_id      TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence       BIGINT NOT NULL CHECK (sequence > 0),
    transaction_id TEXT NOT NULL,
    entry_index    INTEGER NOT NULL CHECK (entry_index > 0),
    account        TEXT NOT NULL,
    asset          TEXT NOT NULL,
    amount         BIGINT NOT NULL CHECK (amount <> 0),
    reference      TEXT NOT NULL,
    PRIMARY KEY (market_id, transaction_id, entry_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_projection_checkpoint (
    market_id   TEXT PRIMARY KEY REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence    BIGINT NOT NULL CHECK (sequence >= 0),
    event_index INTEGER NOT NULL CHECK (event_index >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS trading_user_session (
    session_hash BYTEA PRIMARY KEY,
    account_id   TEXT NOT NULL,
    github_login TEXT NOT NULL,
    csrf_hash    BYTEA NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS trading_user_session_expiry_idx
    ON trading_user_session (expires_at);

COMMIT;
