-- Recovery epochs are a write-admission control plane, not a second trading
-- state. Orders, balances and matching state remain derived from the immutable
-- trading event stream.

CREATE TABLE IF NOT EXISTS trading_recovery_epoch (
    schema_version       INTEGER NOT NULL,
    market_id            TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    epoch_id             TEXT NOT NULL,
    phase                TEXT NOT NULL CHECK (phase IN (
        'bootstrap',
        'dependencies_ready',
        'trading_replay',
        'reconciling',
        'read_only',
        'transport_warmup',
        'writable',
        'offline',
        'manual_review'
    )),
    runtime_sequence     BIGINT NOT NULL DEFAULT 0 CHECK (runtime_sequence >= 0),
    state_hash           TEXT NOT NULL DEFAULT '',
    ledger_balanced      BOOLEAN NOT NULL DEFAULT FALSE,
    event_continuous     BOOLEAN NOT NULL DEFAULT FALSE,
    projection_caught_up BOOLEAN NOT NULL DEFAULT FALSE,
    outbox_caught_up     BOOLEAN NOT NULL DEFAULT FALSE,
    transport_healthy    BOOLEAN NOT NULL DEFAULT FALSE,
    writes_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    last_error           TEXT NOT NULL DEFAULT '',
    version              BIGINT NOT NULL CHECK (version > 0),
    started_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (market_id, epoch_id),
    CHECK (writes_enabled = (phase = 'writable'))
);

CREATE TABLE IF NOT EXISTS trading_recovery_current (
    market_id  TEXT PRIMARY KEY REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    epoch_id   TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (market_id, epoch_id)
        REFERENCES trading_recovery_epoch(market_id, epoch_id)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_recovery_epoch_started_idx
    ON trading_recovery_epoch (market_id, started_at DESC);
