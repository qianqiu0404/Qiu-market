BEGIN;

ALTER TABLE dex_route_current
    ADD COLUMN IF NOT EXISTS quote_reference_kind TEXT NOT NULL DEFAULT 'none';

ALTER TABLE dex_route_current
    DROP CONSTRAINT IF EXISTS dex_route_current_quote_reference_kind_check;

ALTER TABLE dex_route_current
    ADD CONSTRAINT dex_route_current_quote_reference_kind_check
    CHECK (quote_reference_kind IN ('none', 'cex_correlated', 'onchain_only'));

ALTER TABLE dex_quote_observation
    ADD COLUMN IF NOT EXISTS quote_notional_usd NUMERIC(65,18) NOT NULL DEFAULT 10000;

DROP INDEX IF EXISTS dex_quote_observation_window_idx;

CREATE INDEX IF NOT EXISTS dex_quote_observation_window_idx
    ON dex_quote_observation(
        provider,
        asset_guid,
        route_key,
        quote_notional_usd,
        observed_at DESC
    );

COMMIT;
