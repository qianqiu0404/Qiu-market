-- Multi-venue spot catalog and asset composite read model.
--
-- provider_market_candidate is deliberately separate from exchange_symbol:
-- discovery never makes a market tradable or visible by itself. An operator
-- must approve an asset_alias before a candidate can become resolved/enabled.

BEGIN;

INSERT INTO exchange(guid, name, code, is_active)
VALUES
    ('provider-exchange-binance', 'Binance', 'binance', TRUE),
    ('provider-exchange-coinbase', 'Coinbase', 'coinbase', TRUE),
    ('provider-exchange-bybit', 'Bybit', 'bybit', TRUE),
    ('provider-exchange-okx', 'OKX', 'okx', TRUE)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name,
    is_active = TRUE,
    updated_at = CURRENT_TIMESTAMP;

CREATE TABLE IF NOT EXISTS asset_alias (
    provider       TEXT        NOT NULL,
    alias          TEXT        NOT NULL,
    asset_guid     TEXT        NOT NULL REFERENCES asset(guid),
    review_status  TEXT        NOT NULL DEFAULT 'pending',
    reviewed_by    TEXT,
    reviewed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, alias),
    CHECK (provider = lower(provider)),
    CHECK (alias = upper(alias)),
    CHECK (review_status IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX IF NOT EXISTS asset_alias_asset_idx
    ON asset_alias(asset_guid, provider);

CREATE TABLE IF NOT EXISTS provider_market_candidate (
    provider          TEXT        NOT NULL,
    source_symbol     TEXT        NOT NULL,
    market_type       TEXT        NOT NULL,
    base_alias        TEXT        NOT NULL,
    quote_alias       TEXT        NOT NULL,
    upstream_status   TEXT,
    resolution_status TEXT        NOT NULL DEFAULT 'discovered',
    base_asset_guid   TEXT REFERENCES asset(guid),
    quote_asset_guid  TEXT REFERENCES asset(guid),
    rejection_reason  TEXT,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    resolved_at       TIMESTAMPTZ,
    enabled_at        TIMESTAMPTZ,
    raw_metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (provider, source_symbol, market_type),
    CHECK (provider = lower(provider)),
    CHECK (market_type IN ('spot', 'perp')),
    CHECK (resolution_status IN ('discovered', 'resolved', 'enabled', 'ambiguous', 'rejected')),
    CHECK (
        resolution_status NOT IN ('resolved', 'enabled')
        OR (base_asset_guid IS NOT NULL AND quote_asset_guid IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS provider_market_candidate_audit_idx
    ON provider_market_candidate(provider, resolution_status, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS asset_metric_current (
    asset_guid            TEXT        PRIMARY KEY REFERENCES asset(guid),
    provider              TEXT        NOT NULL,
    provider_asset_id     TEXT        NOT NULL,
    market_cap_rank       INTEGER,
    reference_price_usd   NUMERIC(65,18),
    market_cap_usd        NUMERIC(65,18),
    circulating_supply    NUMERIC(65,18),
    total_supply          NUMERIC(65,18),
    max_supply            NUMERIC(65,18),
    image_url             TEXT,
    provider_updated_at   TIMESTAMPTZ,
    observed_at           TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (market_cap_rank IS NULL OR market_cap_rank > 0),
    CHECK (reference_price_usd IS NULL OR reference_price_usd > 0),
    CHECK (market_cap_usd IS NULL OR market_cap_usd >= 0),
    CHECK (circulating_supply IS NULL OR circulating_supply >= 0),
    UNIQUE (provider, provider_asset_id)
);

CREATE INDEX IF NOT EXISTS asset_metric_rank_idx
    ON asset_metric_current(market_cap_rank)
    WHERE market_cap_rank IS NOT NULL;

CREATE TABLE IF NOT EXISTS market_global_metric (
    provider                  TEXT        PRIMARY KEY,
    total_market_cap_usd      NUMERIC(65,18),
    total_volume_24h_usd      NUMERIC(65,18),
    btc_dominance_pct         NUMERIC(65,18),
    provider_updated_at       TIMESTAMPTZ,
    observed_at               TIMESTAMPTZ NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE symbol_market
    ADD COLUMN IF NOT EXISTS open_24h NUMERIC(65,18),
    ADD COLUMN IF NOT EXISTS quote_turnover_24h NUMERIC(65,18),
    ADD COLUMN IF NOT EXISTS quote_turnover_usd NUMERIC(65,18);

CREATE SEQUENCE IF NOT EXISTS asset_price_index_version_seq;

CREATE TABLE IF NOT EXISTS asset_price_index (
    asset_guid          TEXT        PRIMARY KEY REFERENCES asset(guid),
    price_usd           NUMERIC(65,18),
    open_24h_usd        NUMERIC(65,18),
    change_24h_pct      NUMERIC(65,18),
    turnover_24h_usd    NUMERIC(65,18),
    contributor_count   INTEGER     NOT NULL DEFAULT 0,
    confidence          TEXT        NOT NULL DEFAULT 'unknown',
    available           BOOLEAN     NOT NULL DEFAULT FALSE,
    version             BIGINT      NOT NULL DEFAULT nextval('asset_price_index_version_seq'),
    observed_at         TIMESTAMPTZ NOT NULL,
    contributors        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    exclusions          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (contributor_count >= 0),
    CHECK (confidence IN ('unknown', 'low', 'medium', 'high')),
    CHECK (
        (available AND price_usd IS NOT NULL AND price_usd > 0 AND contributor_count > 0)
        OR
        (NOT available AND contributor_count = 0)
    )
);

CREATE INDEX IF NOT EXISTS asset_price_index_rank_idx
    ON asset_price_index(available DESC, turnover_24h_usd DESC NULLS LAST, asset_guid);

-- Existing production markets are already reviewed facts. Seed aliases from
-- canonical asset identities instead of guessing from an arbitrary symbol
-- discovered in a provider payload.
INSERT INTO asset_alias(provider, alias, asset_guid, review_status, reviewed_by, reviewed_at)
SELECT DISTINCT e.code, upper(a.asset_symbol), a.guid, 'approved', 'migration-existing-catalog', clock_timestamp()
FROM exchange_symbol es
JOIN exchange e ON e.guid = es.exchange_guid
JOIN symbol s ON s.guid = es.symbol_guid
JOIN asset a ON a.guid = s.base_asset_guid
WHERE es.is_active = TRUE
ON CONFLICT (provider, alias) DO NOTHING;

INSERT INTO asset_alias(provider, alias, asset_guid, review_status, reviewed_by, reviewed_at)
SELECT DISTINCT e.code, upper(a.asset_symbol), a.guid, 'approved', 'migration-existing-catalog', clock_timestamp()
FROM exchange_symbol es
JOIN exchange e ON e.guid = es.exchange_guid
JOIN symbol s ON s.guid = es.symbol_guid
JOIN asset a ON a.guid = s.qoute_asset_guid
WHERE es.is_active = TRUE
ON CONFLICT (provider, alias) DO NOTHING;

COMMIT;
