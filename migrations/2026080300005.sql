-- Slice 2: one canonical 24h percentage and explicit snapshot observation
-- times. The legacy radio column remains during the verification window and
-- is dual-written from the same canonical value.

BEGIN;

ALTER TABLE symbol_market
    ADD COLUMN IF NOT EXISTS change_24h_pct NUMERIC(65,18),
    ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source_time TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source_time_kind TEXT;

CREATE INDEX IF NOT EXISTS symbol_market_observed_at_idx
    ON symbol_market(observed_at DESC);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM symbol_market
        WHERE source_time IS NULL AND source_time_kind IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'symbol_market source time audit failed: kind exists without time';
    END IF;
END $$;

COMMIT;
