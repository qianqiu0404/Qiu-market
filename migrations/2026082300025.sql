-- Separate the transactional outbox from the browser cursor event feed.
--
-- The trading process publishes committed outbox rows into trading_event_feed,
-- advances a durable checkpoint, then marks the source rows published in the
-- same PostgreSQL transaction. Published source rows are retained for 24 hours
-- before bounded cleanup; the cursor feed remains available for replay.

BEGIN;

CREATE TABLE IF NOT EXISTS trading_event_feed (
    market_id       TEXT NOT NULL,
    sequence        BIGINT NOT NULL CHECK (sequence > 0),
    event_index     INTEGER NOT NULL CHECK (event_index > 0),
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    published_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (market_id, sequence, event_index),
    FOREIGN KEY (market_id, sequence)
        REFERENCES trading_event_batch(market_id, sequence)
        ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS trading_outbox_checkpoint (
    market_id       TEXT PRIMARY KEY REFERENCES trading_market(market_id) ON DELETE RESTRICT,
    sequence        BIGINT NOT NULL CHECK (sequence >= 0),
    event_index     INTEGER NOT NULL CHECK (event_index >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO trading_event_feed (
    market_id, sequence, event_index, event_type, payload, published_at
)
SELECT
    market_id,
    sequence,
    event_index,
    event_type,
    payload,
    published_at
FROM trading_outbox
WHERE published_at IS NOT NULL
ON CONFLICT (market_id, sequence, event_index) DO NOTHING;

INSERT INTO trading_outbox_checkpoint (
    market_id, sequence, event_index, updated_at
)
SELECT DISTINCT ON (market_id)
    market_id,
    sequence,
    event_index,
    clock_timestamp()
FROM trading_event_feed
ORDER BY market_id, sequence DESC, event_index DESC
ON CONFLICT (market_id) DO UPDATE
SET sequence=EXCLUDED.sequence,
    event_index=EXCLUDED.event_index,
    updated_at=EXCLUDED.updated_at
WHERE (
    trading_outbox_checkpoint.sequence,
    trading_outbox_checkpoint.event_index
) < (EXCLUDED.sequence, EXCLUDED.event_index);

COMMIT;
