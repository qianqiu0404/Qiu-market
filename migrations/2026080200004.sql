-- Slice 1: stable market identity, explicit K-line time semantics and a
-- content-change sequence for the shadow Doris v2 pipeline.
--
-- Important legacy fact: symbol_kline.created_at was explicitly populated by
-- the crawler with the Binance open time. It is therefore business time, not
-- ingestion time, even though the Go model carries GORM's autoCreateTime tag.

BEGIN;

ALTER TABLE exchange ADD COLUMN IF NOT EXISTS code VARCHAR(100);

UPDATE exchange
SET code = trim(BOTH '-' FROM regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g'))
WHERE code IS NULL OR btrim(code) = '';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM exchange
        WHERE code IS NULL
           OR code !~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    ) THEN
        RAISE EXCEPTION 'exchange.code audit failed: NULL or non-canonical code exists';
    END IF;
    IF EXISTS (SELECT code FROM exchange GROUP BY code HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'exchange.code audit failed: duplicate code exists';
    END IF;
END $$;

ALTER TABLE exchange ALTER COLUMN code SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS exchange_code_uidx ON exchange(code);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'exchange'::regclass
          AND conname = 'exchange_code_format_chk'
    ) THEN
        ALTER TABLE exchange
            ADD CONSTRAINT exchange_code_format_chk
            CHECK (code ~ '^[a-z0-9]+(-[a-z0-9]+)*$');
    END IF;
END $$;

ALTER TABLE exchange_symbol ADD COLUMN IF NOT EXISTS market_code TEXT;

UPDATE exchange_symbol es
SET market_code = e.code || ':' || upper(s.symbol_name) || ':' || lower(s.market_type)
FROM exchange e, symbol s
WHERE es.exchange_guid = e.guid
  AND es.symbol_guid = s.guid
  AND (es.market_code IS NULL OR btrim(es.market_code) = '');

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM exchange_symbol
        WHERE market_code IS NULL
           OR market_code !~ '^[a-z0-9]+:[A-Z0-9][A-Z0-9._-]*/[A-Z0-9][A-Z0-9._-]*:[a-z0-9-]+$'
    ) THEN
        RAISE EXCEPTION 'exchange_symbol.market_code audit failed: NULL or non-canonical value exists';
    END IF;
    IF EXISTS (SELECT market_code FROM exchange_symbol GROUP BY market_code HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'exchange_symbol.market_code audit failed: duplicate value exists';
    END IF;
    IF EXISTS (
        SELECT exchange_guid, symbol_guid
        FROM exchange_symbol
        GROUP BY exchange_guid, symbol_guid
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'exchange_symbol identity audit failed: duplicate (exchange_guid, symbol_guid) exists';
    END IF;
END $$;

ALTER TABLE exchange_symbol ALTER COLUMN market_code SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS exchange_symbol_market_code_uidx
    ON exchange_symbol(market_code);
CREATE UNIQUE INDEX IF NOT EXISTS exchange_symbol_exchange_symbol_uidx
    ON exchange_symbol(exchange_guid, symbol_guid);

ALTER TABLE symbol_market ADD COLUMN IF NOT EXISTS market_id TEXT;

WITH unique_active_market AS (
    SELECT symbol_guid, min(guid) AS market_id
    FROM exchange_symbol
    WHERE is_active = true
    GROUP BY symbol_guid
    HAVING count(*) = 1
)
UPDATE symbol_market sm
SET market_id = u.market_id
FROM unique_active_market u
WHERE sm.symbol_guid = u.symbol_guid
  AND (sm.market_id IS NULL OR sm.market_id = '');

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM symbol_market WHERE market_id IS NULL OR market_id = '') THEN
        RAISE EXCEPTION 'symbol_market.market_id audit failed: unresolved or multi-venue symbol exists';
    END IF;
    IF EXISTS (SELECT market_id FROM symbol_market GROUP BY market_id HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'symbol_market.market_id audit failed: duplicate current-market row exists';
    END IF;
END $$;

ALTER TABLE symbol_market ALTER COLUMN market_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS symbol_market_market_id_uidx ON symbol_market(market_id);

ALTER TABLE symbol_kline ADD COLUMN IF NOT EXISTS market_id TEXT;
ALTER TABLE symbol_kline ADD COLUMN IF NOT EXISTS open_time TIMESTAMPTZ;
ALTER TABLE symbol_kline ADD COLUMN IF NOT EXISTS ingested_at TIMESTAMPTZ;
ALTER TABLE symbol_kline ADD COLUMN IF NOT EXISTS sync_seq BIGINT;

WITH unique_active_market AS (
    SELECT symbol_guid, min(guid) AS market_id
    FROM exchange_symbol
    WHERE is_active = true
    GROUP BY symbol_guid
    HAVING count(*) = 1
)
UPDATE symbol_kline sk
SET market_id = u.market_id
FROM unique_active_market u
WHERE sk.symbol_guid = u.symbol_guid
  AND (sk.market_id IS NULL OR sk.market_id = '');

-- K-line GUIDs end in the upstream millisecond open time. Parse the suffix as
-- the migration source of truth, then audit it against legacy created_at.
UPDATE symbol_kline
SET open_time = to_timestamp((substring(guid FROM '([0-9]{13})$'))::double precision / 1000.0)
WHERE open_time IS NULL
  AND guid ~ '[0-9]{13}$';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM symbol_kline
        WHERE market_id IS NULL OR market_id = '' OR open_time IS NULL
    ) THEN
        RAISE EXCEPTION 'symbol_kline backfill audit failed: unresolved market_id or GUID open_time';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM symbol_kline
        WHERE abs(extract(epoch FROM (
            open_time - (created_at AT TIME ZONE 'Asia/Shanghai')
        ))) > 1
    ) THEN
        RAISE EXCEPTION 'symbol_kline time audit failed: GUID suffix disagrees with legacy created_at';
    END IF;
    IF EXISTS (
        SELECT market_id, "interval", open_time
        FROM symbol_kline
        GROUP BY market_id, "interval", open_time
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'symbol_kline identity audit failed: duplicate business key exists';
    END IF;
END $$;

-- First-ingestion time was not recorded historically. For migrated rows this
-- is the best available approximation and is deliberately documented as such.
UPDATE symbol_kline
SET ingested_at = COALESCE(updated_at, created_at) AT TIME ZONE 'Asia/Shanghai'
WHERE ingested_at IS NULL;

CREATE SEQUENCE IF NOT EXISTS symbol_kline_sync_seq;

UPDATE symbol_kline
SET sync_seq = nextval('symbol_kline_sync_seq')
WHERE sync_seq IS NULL;

SELECT setval(
    'symbol_kline_sync_seq',
    GREATEST(
        (SELECT last_value FROM symbol_kline_sync_seq),
        COALESCE((SELECT max(sync_seq) FROM symbol_kline), 0),
        1
    ),
    EXISTS (SELECT 1 FROM symbol_kline)
);

ALTER TABLE symbol_kline
    ALTER COLUMN ingested_at SET DEFAULT clock_timestamp(),
    ALTER COLUMN sync_seq SET DEFAULT nextval('symbol_kline_sync_seq');

CREATE OR REPLACE FUNCTION symbol_kline_assign_sync_seq()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.sync_seq IS NULL THEN
            NEW.sync_seq := nextval('symbol_kline_sync_seq');
        END IF;
        IF NEW.ingested_at IS NULL THEN
            NEW.ingested_at := clock_timestamp();
        END IF;
        RETURN NEW;
    END IF;

    IF ROW(
        NEW.market_id, NEW."interval", NEW.open_time,
        NEW.open_price, NEW.high_price, NEW.low_price, NEW.close_price,
        NEW.volume, NEW.market_cap, NEW.is_active
    ) IS DISTINCT FROM ROW(
        OLD.market_id, OLD."interval", OLD.open_time,
        OLD.open_price, OLD.high_price, OLD.low_price, OLD.close_price,
        OLD.volume, OLD.market_cap, OLD.is_active
    ) THEN
        NEW.sync_seq := nextval('symbol_kline_sync_seq');
        NEW.updated_at := clock_timestamp();
    ELSE
        NEW.sync_seq := OLD.sync_seq;
        NEW.updated_at := OLD.updated_at;
    END IF;
    NEW.ingested_at := OLD.ingested_at;
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS symbol_kline_assign_sync_seq_trigger ON symbol_kline;
CREATE TRIGGER symbol_kline_assign_sync_seq_trigger
BEFORE INSERT OR UPDATE ON symbol_kline
FOR EACH ROW EXECUTE FUNCTION symbol_kline_assign_sync_seq();

ALTER TABLE symbol_kline
    ALTER COLUMN market_id SET NOT NULL,
    ALTER COLUMN open_time SET NOT NULL,
    ALTER COLUMN ingested_at SET NOT NULL,
    ALTER COLUMN sync_seq SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS symbol_kline_business_key_uidx
    ON symbol_kline(market_id, "interval", open_time);
CREATE INDEX IF NOT EXISTS symbol_kline_sync_seq_idx ON symbol_kline(sync_seq);
CREATE INDEX IF NOT EXISTS symbol_kline_market_interval_open_idx
    ON symbol_kline(market_id, "interval", open_time DESC);

ALTER TABLE dw_sync_state
    ADD COLUMN IF NOT EXISTS last_sync_seq BIGINT NOT NULL DEFAULT 0;

COMMIT;
