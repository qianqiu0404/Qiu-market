-- Freeze every canary rollout to an explicit, auditable ten-asset boundary.
-- A legacy row with an empty list is migrated only when the currently enabled
-- provider markets resolve to exactly ten distinct, reviewed Top 50 assets.

BEGIN;

CREATE OR REPLACE FUNCTION rollout_has_exactly_ten_unique_assets(value JSONB)
RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
AS $$
    SELECT jsonb_typeof(value) = 'array'
       AND jsonb_array_length(value) = 10
       AND (
            SELECT COUNT(DISTINCT asset_id)
            FROM jsonb_array_elements_text(value) AS asset_id
       ) = 10
       AND NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements_text(value) AS asset_id
            WHERE btrim(asset_id) = ''
       );
$$;

DO $$
DECLARE
    rollout RECORD;
    enabled_assets JSONB;
    enabled_count INTEGER;
    candidate_count INTEGER;
    invalid_assets TEXT;
BEGIN
    FOR rollout IN
        SELECT provider, rank_limit, canary_asset_ids
        FROM provider_rollout_state
        WHERE mode = 'canary'
        FOR UPDATE
    LOOP
        IF rollout_has_exactly_ten_unique_assets(rollout.canary_asset_ids) THEN
            CONTINUE;
        END IF;

        IF rollout.canary_asset_ids <> '[]'::jsonb THEN
            RAISE EXCEPTION
                'provider % has an invalid non-empty canary_asset_ids value; expected exactly 10 unique assets',
                rollout.provider;
        END IF;

        SELECT
            COALESCE(
                jsonb_agg(asset_id ORDER BY market_cap_rank, asset_id),
                '[]'::jsonb
            ),
            COUNT(*),
            string_agg(asset_id, ',' ORDER BY market_cap_rank, asset_id)
                FILTER (WHERE market_cap_rank IS NULL
                    OR market_cap_rank NOT BETWEEN 1 AND rollout.rank_limit
                    OR NOT identity_approved)
        INTO enabled_assets, enabled_count, invalid_assets
        FROM (
            SELECT DISTINCT ON (symbol_row.base_asset_guid)
                symbol_row.base_asset_guid AS asset_id,
                metric.market_cap_rank,
                (
                    candidate.resolution_status IN ('resolved', 'enabled')
                    AND candidate.base_asset_guid = symbol_row.base_asset_guid
                ) AS identity_approved
            FROM exchange_symbol market
            JOIN exchange venue
              ON venue.guid = market.exchange_guid
             AND venue.code = rollout.provider
            JOIN symbol symbol_row
              ON symbol_row.guid = market.symbol_guid
             AND lower(symbol_row.market_type) = 'spot'
             AND symbol_row.is_active = TRUE
            JOIN provider_market_candidate candidate
              ON candidate.provider = rollout.provider
             AND candidate.market_type = 'spot'
             AND candidate.source_symbol = market.source_symbol
            LEFT JOIN asset_metric_current metric
              ON metric.asset_guid = symbol_row.base_asset_guid
            WHERE market.is_active = TRUE
            ORDER BY symbol_row.base_asset_guid, metric.market_cap_rank
        ) AS enabled;

        SELECT COUNT(*)
        INTO candidate_count
        FROM provider_market_candidate
        WHERE provider = rollout.provider;

        -- A brand-new database has no catalog evidence to preserve. It must
        -- begin in shadow rather than carrying an impossible empty canary.
        -- Once any candidate/market exists, the strict ten-asset audit below
        -- applies and never silently demotes a real rollout.
        IF candidate_count = 0 AND enabled_count = 0 THEN
            UPDATE provider_rollout_state
            SET mode = 'shadow',
                canary_asset_ids = '[]'::jsonb,
                min_soak_until = NULL,
                updated_at = clock_timestamp()
            WHERE provider = rollout.provider
              AND mode = 'canary'
              AND canary_asset_ids = '[]'::jsonb;
            CONTINUE;
        END IF;

        IF enabled_count <> 10 OR invalid_assets IS NOT NULL THEN
            RAISE EXCEPTION
                'provider % legacy canary audit failed: enabled_asset_count=%, invalid_assets=%',
                rollout.provider,
                enabled_count,
                COALESCE(invalid_assets, '<none>');
        END IF;

        UPDATE provider_rollout_state
        SET canary_asset_ids = enabled_assets,
            updated_at = clock_timestamp()
        WHERE provider = rollout.provider
          AND mode = 'canary'
          AND canary_asset_ids = '[]'::jsonb;
    END LOOP;
END;
$$;

ALTER TABLE provider_rollout_state
    DROP CONSTRAINT IF EXISTS provider_rollout_canary_assets_check;

ALTER TABLE provider_rollout_state
    ADD CONSTRAINT provider_rollout_canary_assets_check
    CHECK (
        mode <> 'canary'
        OR rollout_has_exactly_ten_unique_assets(canary_asset_ids)
    );

COMMIT;
