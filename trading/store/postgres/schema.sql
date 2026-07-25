CREATE TABLE IF NOT EXISTS trading_market (
    market_id              text PRIMARY KEY,
    base_asset             text NOT NULL,
    quote_asset            text NOT NULL,
    base_scale             bigint NOT NULL CHECK (base_scale > 0),
    quote_scale            bigint NOT NULL CHECK (quote_scale > 0),
    price_tick             bigint NOT NULL CHECK (price_tick > 0),
    quantity_step          bigint NOT NULL CHECK (quantity_step > 0),
    min_quantity           bigint NOT NULL CHECK (min_quantity > 0),
    min_notional           bigint NOT NULL CHECK (min_notional > 0),
    maker_fee_bps          bigint NOT NULL CHECK (maker_fee_bps >= 0 AND maker_fee_bps < 10000),
    taker_fee_bps          bigint NOT NULL CHECK (taker_fee_bps >= 0 AND taker_fee_bps < 10000),
    configuration_epoch    bigint NOT NULL CHECK (configuration_epoch > 0),
    current_sequence       bigint NOT NULL DEFAULT 0 CHECK (current_sequence >= 0),
    enabled                boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trading_event_batch (
    market_id       text NOT NULL REFERENCES trading_market(market_id),
    sequence        bigint NOT NULL CHECK (sequence > 0),
    schema_version  smallint NOT NULL CHECK (schema_version > 0),
    operation       smallint NOT NULL CHECK (operation > 0),
    account_id      text NOT NULL,
    request_id      text NOT NULL,
    fingerprint     text NOT NULL,
    command_payload jsonb NOT NULL,
    result_payload  jsonb NOT NULL,
    journal_payload jsonb NOT NULL,
    projection_payload jsonb NOT NULL,
    state_hash      text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (market_id, sequence),
    UNIQUE (market_id, account_id, operation, request_id)
);

ALTER TABLE trading_event_batch
    ADD COLUMN IF NOT EXISTS projection_payload jsonb NOT NULL
    DEFAULT '{"orders":[],"trades":[],"balances":[]}'::jsonb;
ALTER TABLE trading_event_batch
    ALTER COLUMN projection_payload DROP DEFAULT;

CREATE TABLE IF NOT EXISTS trading_snapshot (
    market_id       text PRIMARY KEY REFERENCES trading_market(market_id),
    schema_version  smallint NOT NULL CHECK (schema_version > 0),
    sequence        bigint NOT NULL CHECK (sequence >= 0),
    state_hash      text NOT NULL,
    payload         bytea NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trading_outbox (
    market_id       text NOT NULL,
    sequence        bigint NOT NULL,
    event_index     integer NOT NULL CHECK (event_index > 0),
    event_type      text NOT NULL,
    payload         jsonb NOT NULL,
    published_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (market_id, sequence, event_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_outbox_unpublished_idx
    ON trading_outbox (market_id, sequence, event_index)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS trading_order (
    market_id       text NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    order_id        text NOT NULL,
    account_id      text NOT NULL,
    status          text NOT NULL,
    updated_sequence bigint NOT NULL CHECK (updated_sequence > 0),
    payload         jsonb NOT NULL,
    PRIMARY KEY (market_id, order_id),
    FOREIGN KEY (market_id, updated_sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_order_account_idx
    ON trading_order (market_id, account_id, updated_sequence DESC);

CREATE TABLE IF NOT EXISTS trading_trade (
    market_id       text NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    trade_id        text NOT NULL,
    sequence        bigint NOT NULL CHECK (sequence > 0),
    event_index     integer NOT NULL CHECK (event_index > 0),
    buyer_account_id text NOT NULL,
    seller_account_id text NOT NULL,
    payload         jsonb NOT NULL,
    PRIMARY KEY (market_id, trade_id),
    UNIQUE (market_id, sequence, event_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_balance (
    market_id       text NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    account_id      text NOT NULL,
    asset           text NOT NULL,
    available       bigint NOT NULL CHECK (available >= 0),
    held            bigint NOT NULL CHECK (held >= 0),
    updated_sequence bigint NOT NULL CHECK (updated_sequence > 0),
    PRIMARY KEY (market_id, account_id, asset),
    FOREIGN KEY (market_id, updated_sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_ledger_entry (
    market_id       text NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence        bigint NOT NULL CHECK (sequence > 0),
    transaction_id  text NOT NULL,
    entry_index     integer NOT NULL CHECK (entry_index > 0),
    account         text NOT NULL,
    asset           text NOT NULL,
    amount          bigint NOT NULL CHECK (amount <> 0),
    reference       text NOT NULL,
    PRIMARY KEY (market_id, transaction_id, entry_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_projection_checkpoint (
    market_id       text PRIMARY KEY REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence        bigint NOT NULL CHECK (sequence >= 0),
    event_index     integer NOT NULL CHECK (event_index >= 0),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trading_user_session (
    session_hash    bytea PRIMARY KEY,
    account_id      text NOT NULL,
    github_login    text NOT NULL,
    csrf_hash       bytea NOT NULL,
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS trading_user_session_expiry_idx
    ON trading_user_session (expires_at);
