-- Trade Product V1 read models. Event batches remain the authority; every
-- object below is rebuildable and uses immutable keyset ordering.

BEGIN;

ALTER TABLE trading_order
    ADD COLUMN IF NOT EXISTS accepted_sequence BIGINT,
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

UPDATE trading_order AS destination
SET accepted_sequence = (destination.payload->>'accepted_sequence')::BIGINT
WHERE destination.accepted_sequence IS NULL;

UPDATE trading_order AS destination
SET created_at = accepted.created_at
FROM trading_event_batch AS accepted
WHERE destination.market_id = accepted.market_id
  AND destination.accepted_sequence = accepted.sequence
  AND destination.created_at IS NULL;

UPDATE trading_order AS destination
SET updated_at = changed.created_at
FROM trading_event_batch AS changed
WHERE destination.market_id = changed.market_id
  AND destination.updated_sequence = changed.sequence
  AND destination.updated_at IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM trading_order
        WHERE accepted_sequence IS NULL OR created_at IS NULL OR updated_at IS NULL
    ) THEN
        RAISE EXCEPTION 'Trade V1 order backfill is incomplete; rebuild from trading_event_batch before retrying';
    END IF;
END
$$;

ALTER TABLE trading_order
    ALTER COLUMN accepted_sequence SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE trading_order
    DROP CONSTRAINT IF EXISTS trading_order_accepted_sequence_check;
ALTER TABLE trading_order
    ADD CONSTRAINT trading_order_accepted_sequence_check
        CHECK (accepted_sequence > 0);

ALTER TABLE trading_order
    DROP CONSTRAINT IF EXISTS trading_order_accepted_sequence_fkey;
ALTER TABLE trading_order
    ADD CONSTRAINT trading_order_accepted_sequence_fkey
        FOREIGN KEY (market_id, accepted_sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS trading_order_account_accepted_idx
    ON trading_order (
        market_id, account_id, accepted_sequence DESC, order_id DESC
    );

CREATE TABLE IF NOT EXISTS trading_order_event (
    market_id       TEXT NOT NULL REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    account_id      TEXT NOT NULL,
    order_id        TEXT NOT NULL,
    sequence        BIGINT NOT NULL CHECK (sequence > 0),
    event_index     INTEGER NOT NULL CHECK (event_index > 0),
    timeline_index  INTEGER NOT NULL CHECK (timeline_index >= 0),
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (market_id, order_id, sequence, event_index, timeline_index),
    UNIQUE (market_id, sequence, event_index, timeline_index),
    FOREIGN KEY (market_id, order_id)
        REFERENCES trading_order(market_id, order_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS trading_order_event_account_idx
    ON trading_order_event (
        market_id, account_id, order_id,
        sequence ASC, event_index ASC, timeline_index ASC
    );

CREATE TABLE IF NOT EXISTS trading_order_event_checkpoint (
    market_id       TEXT PRIMARY KEY REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence        BIGINT NOT NULL CHECK (sequence >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS trading_trade_buyer_cursor_idx
    ON trading_trade (
        market_id, buyer_account_id,
        sequence DESC, event_index DESC, trade_id DESC
    );

CREATE INDEX IF NOT EXISTS trading_trade_seller_cursor_idx
    ON trading_trade (
        market_id, seller_account_id,
        sequence DESC, event_index DESC, trade_id DESC
    );

CREATE INDEX IF NOT EXISTS trading_ledger_account_cursor_idx
    ON trading_ledger_entry (
        market_id, account,
        sequence DESC, transaction_id DESC, entry_index DESC
    );

COMMIT;
