-- Trade Product V1 read models. Event batches remain the authority; every
-- object below is rebuildable and uses immutable keyset ordering.

BEGIN;

ALTER TABLE trading_order
    ADD COLUMN IF NOT EXISTS accepted_sequence BIGINT,
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

-- Keep the currently running pre-V1 binary and the rollback binary writable.
-- Those versions still insert the original six columns; the trigger derives
-- V1 ordering/timestamps from the immutable payload and event batch.
CREATE OR REPLACE FUNCTION trading_order_v1_compat_columns()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.accepted_sequence IS NULL THEN
        NEW.accepted_sequence := (NEW.payload->>'accepted_sequence')::BIGINT;
    END IF;
    IF NEW.created_at IS NULL THEN
        SELECT source.created_at
        INTO NEW.created_at
        FROM trading_event_batch AS source
        WHERE source.market_id = NEW.market_id
          AND source.sequence = NEW.accepted_sequence;
    END IF;
    IF TG_OP = 'INSERT' THEN
        SELECT source.created_at
        INTO NEW.updated_at
        FROM trading_event_batch AS source
        WHERE source.market_id = NEW.market_id
          AND source.sequence = NEW.updated_sequence;
    ELSIF NEW.updated_sequence IS DISTINCT FROM OLD.updated_sequence
       OR NEW.updated_at IS NULL THEN
        SELECT source.created_at
        INTO NEW.updated_at
        FROM trading_event_batch AS source
        WHERE source.market_id = NEW.market_id
          AND source.sequence = NEW.updated_sequence;
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS trading_order_v1_compat_columns_trigger ON trading_order;
CREATE TRIGGER trading_order_v1_compat_columns_trigger
BEFORE INSERT OR UPDATE ON trading_order
FOR EACH ROW
EXECUTE FUNCTION trading_order_v1_compat_columns();

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
    row_count       BIGINT NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- Existing V1 candidates may already have a sequence-only checkpoint. Populate
-- its lifecycle cardinality exactly once; subsequent runs must preserve the
-- stored value so runtime integrity checks can detect deleted or extra rows.
ALTER TABLE trading_order_event_checkpoint
    ADD COLUMN IF NOT EXISTS row_count BIGINT;

UPDATE trading_order_event_checkpoint AS checkpoint
SET row_count = (
    SELECT count(*)
    FROM trading_order_event AS event
    WHERE event.market_id = checkpoint.market_id
)
WHERE checkpoint.row_count IS NULL;

ALTER TABLE trading_order_event_checkpoint
    ALTER COLUMN row_count SET DEFAULT 0,
    ALTER COLUMN row_count SET NOT NULL;

ALTER TABLE trading_order_event_checkpoint
    DROP CONSTRAINT IF EXISTS trading_order_event_checkpoint_row_count_check;
ALTER TABLE trading_order_event_checkpoint
    ADD CONSTRAINT trading_order_event_checkpoint_row_count_check
        CHECK (row_count >= 0);

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
