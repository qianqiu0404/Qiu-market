-- Top-50 multi-provider rollout, venue-level asset snapshots, and audited
-- on-chain DEX identities. Discovery and shadow observations remain separate
-- from operator-approved activation.

BEGIN;

ALTER TABLE asset_alias
    ADD COLUMN IF NOT EXISTS review_source TEXT,
    ADD COLUMN IF NOT EXISTS review_note TEXT;

CREATE TABLE IF NOT EXISTS provider_rollout_state (
    provider              TEXT        PRIMARY KEY,
    mode                  TEXT        NOT NULL DEFAULT 'shadow',
    rank_limit            INTEGER     NOT NULL DEFAULT 50,
    canary_asset_ids      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    min_soak_until        TIMESTAMPTZ,
    last_transition_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_error            TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (provider = lower(provider)),
    CHECK (mode IN ('shadow', 'canary', 'enabled', 'paused')),
    CHECK (rank_limit BETWEEN 1 AND 200),
    CHECK (jsonb_typeof(canary_asset_ids) = 'array')
);

INSERT INTO provider_rollout_state(provider, mode, rank_limit, min_soak_until)
VALUES
    -- A clean install has no reviewed/enabled market set yet, so it must
    -- begin in shadow. Existing deployments keep their current row because
    -- ON CONFLICT never overwrites rollout state.
    ('binance', 'shadow', 50, NULL),
    ('coinbase', 'shadow', 50, NULL),
    ('bybit', 'shadow', 50, NULL),
    ('okx', 'shadow', 50, NULL),
    ('hyperliquid', 'enabled', 50, NULL),
    ('uniswap', 'shadow', 50, NULL),
    ('pancakeswap', 'shadow', 50, NULL)
ON CONFLICT (provider) DO NOTHING;

CREATE SEQUENCE IF NOT EXISTS asset_venue_snapshot_version_seq;

CREATE TABLE IF NOT EXISTS asset_venue_snapshot (
    asset_guid          TEXT        NOT NULL REFERENCES asset(guid),
    provider            TEXT        NOT NULL,
    price_kind          TEXT        NOT NULL,
    price_usd           NUMERIC(65,18),
    open_24h_usd        NUMERIC(65,18),
    change_24h_pct      NUMERIC(65,18),
    turnover_24h_usd    NUMERIC(65,18),
    contributor_count   INTEGER     NOT NULL DEFAULT 0,
    market_count        INTEGER     NOT NULL DEFAULT 0,
    confidence          TEXT        NOT NULL DEFAULT 'unknown',
    quality             TEXT        NOT NULL DEFAULT 'unknown',
    available           BOOLEAN     NOT NULL DEFAULT FALSE,
    source_time         TIMESTAMPTZ,
    observed_at         TIMESTAMPTZ NOT NULL,
    version             BIGINT      NOT NULL DEFAULT nextval('asset_venue_snapshot_version_seq'),
    metadata            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (asset_guid, provider, price_kind),
    CHECK (provider = lower(provider)),
    CHECK (price_kind IN ('composite_spot', 'venue_spot', 'perp_mark', 'dex_route')),
    CHECK (confidence IN ('unknown', 'low', 'medium', 'high')),
    CHECK (quality IN ('unknown', 'low', 'medium', 'high')),
    CHECK (contributor_count >= 0),
    CHECK (market_count >= 0),
    CHECK (
        (available AND price_usd IS NOT NULL AND price_usd > 0)
        OR
        (NOT available)
    )
);

CREATE INDEX IF NOT EXISTS asset_venue_snapshot_provider_idx
    ON asset_venue_snapshot(provider, price_kind, available, turnover_24h_usd DESC NULLS LAST);

CREATE TABLE IF NOT EXISTS blockchain_network (
    chain_id        BIGINT      PRIMARY KEY,
    code            TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    native_symbol   TEXT        NOT NULL,
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (chain_id > 0),
    CHECK (code = lower(code))
);

INSERT INTO blockchain_network(chain_id, code, name, native_symbol)
VALUES
    (1, 'ethereum', 'Ethereum Mainnet', 'ETH'),
    (56, 'bsc', 'BNB Smart Chain', 'BNB')
ON CONFLICT (chain_id) DO UPDATE
SET code = EXCLUDED.code,
    name = EXCLUDED.name,
    native_symbol = EXCLUDED.native_symbol,
    is_active = TRUE,
    updated_at = clock_timestamp();

CREATE TABLE IF NOT EXISTS asset_representation (
    asset_guid          TEXT        NOT NULL REFERENCES asset(guid),
    chain_id            BIGINT      NOT NULL REFERENCES blockchain_network(chain_id),
    contract_address    TEXT        NOT NULL,
    representation_kind TEXT        NOT NULL,
    token_symbol        TEXT        NOT NULL,
    decimals            INTEGER     NOT NULL,
    review_status       TEXT        NOT NULL DEFAULT 'pending',
    review_source       TEXT,
    review_note         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, contract_address),
    UNIQUE (asset_guid, chain_id, contract_address),
    CHECK (contract_address = lower(contract_address)),
    CHECK (contract_address ~ '^0x[0-9a-f]{40}$'),
    CHECK (representation_kind IN ('native_wrapped', 'canonical', 'wrapped', 'bridged')),
    CHECK (review_status IN ('pending', 'approved', 'rejected')),
    CHECK (decimals BETWEEN 0 AND 36)
);

CREATE INDEX IF NOT EXISTS asset_representation_asset_idx
    ON asset_representation(asset_guid, chain_id, review_status);

CREATE TABLE IF NOT EXISTS dex_pool_candidate (
    provider          TEXT        NOT NULL,
    chain_id          BIGINT      NOT NULL REFERENCES blockchain_network(chain_id),
    protocol_version  TEXT        NOT NULL,
    pool_address      TEXT        NOT NULL,
    token0_address    TEXT        NOT NULL,
    token1_address    TEXT        NOT NULL,
    fee_tier          INTEGER     NOT NULL,
    resolution_status TEXT        NOT NULL DEFAULT 'discovered',
    rejection_reason  TEXT,
    tvl_usd           NUMERIC(65,18),
    volume_24h_usd    NUMERIC(65,18),
    block_number      BIGINT,
    block_timestamp   TIMESTAMPTZ,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    raw_metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (provider, chain_id, pool_address),
    CHECK (provider IN ('uniswap', 'pancakeswap')),
    CHECK (pool_address = lower(pool_address)),
    CHECK (token0_address = lower(token0_address)),
    CHECK (token1_address = lower(token1_address)),
    CHECK (resolution_status IN ('discovered', 'resolved', 'enabled', 'ambiguous', 'rejected')),
    CHECK (fee_tier > 0)
);

CREATE INDEX IF NOT EXISTS dex_pool_candidate_audit_idx
    ON dex_pool_candidate(provider, resolution_status, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS dex_route_current (
    provider            TEXT        NOT NULL,
    asset_guid          TEXT        NOT NULL REFERENCES asset(guid),
    route_key           TEXT        NOT NULL,
    chain_id            BIGINT      NOT NULL REFERENCES blockchain_network(chain_id),
    price_usd           NUMERIC(65,18),
    buy_price_usd       NUMERIC(65,18),
    sell_price_usd      NUMERIC(65,18),
    change_24h_pct      NUMERIC(65,18),
    turnover_24h_usd    NUMERIC(65,18),
    tvl_usd             NUMERIC(65,18),
    price_impact_pct    NUMERIC(65,18),
    round_trip_spread_pct NUMERIC(65,18),
    quote_notional_usd  NUMERIC(65,18) NOT NULL DEFAULT 10000,
    block_number        BIGINT,
    block_timestamp     TIMESTAMPTZ,
    quality             TEXT        NOT NULL DEFAULT 'unknown',
    available           BOOLEAN     NOT NULL DEFAULT FALSE,
    path                JSONB       NOT NULL DEFAULT '[]'::jsonb,
    pool_addresses      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    unavailable_reason  TEXT,
    observed_at         TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, asset_guid, route_key),
    CHECK (provider IN ('uniswap', 'pancakeswap')),
    CHECK (quality IN ('unknown', 'low', 'medium', 'high')),
    CHECK (jsonb_typeof(path) = 'array'),
    CHECK (jsonb_typeof(pool_addresses) = 'array')
);

CREATE INDEX IF NOT EXISTS dex_route_current_asset_idx
    ON dex_route_current(asset_guid, provider, available, quality);

CREATE TABLE IF NOT EXISTS dex_quote_observation (
    provider        TEXT        NOT NULL,
    asset_guid      TEXT        NOT NULL REFERENCES asset(guid),
    route_key       TEXT        NOT NULL,
    observed_at     TIMESTAMPTZ NOT NULL,
    price_usd       NUMERIC(65,18) NOT NULL,
    block_number    BIGINT,
    PRIMARY KEY (provider, asset_guid, route_key, observed_at)
);

CREATE INDEX IF NOT EXISTS dex_quote_observation_window_idx
    ON dex_quote_observation(provider, asset_guid, observed_at DESC);

COMMIT;
