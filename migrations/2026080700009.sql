-- Repair one reviewed legacy identity boundary: the pre-existing a3 asset is
-- canonical USDT. Earlier CoinGecko bootstrap migrations did not seed the
-- tether external mapping, so the first Top 200 refresh could create a second
-- USDT asset before the code-reviewed manifest was applied.

BEGIN;

CREATE TABLE IF NOT EXISTS asset_identity_merge_audit (
    provider       TEXT        NOT NULL,
    external_id    TEXT        NOT NULL,
    from_asset_guid TEXT       NOT NULL,
    to_asset_guid   TEXT       NOT NULL,
    reason         TEXT        NOT NULL,
    migrated_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, external_id, from_asset_guid, to_asset_guid)
);

DO $$
DECLARE
    duplicate_asset_guid TEXT;
BEGIN
    SELECT mapping.asset_guid
    INTO duplicate_asset_guid
    FROM asset_external_mapping mapping
    WHERE mapping.provider = 'coingecko'
      AND mapping.external_id = 'tether'
      AND mapping.asset_guid <> 'a3'
    LIMIT 1;

    IF duplicate_asset_guid IS NULL THEN
        RETURN;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM asset
        WHERE guid = 'a3'
          AND upper(asset_symbol) = 'USDT'
    ) THEN
        RAISE EXCEPTION
            'reviewed USDT identity repair stopped: canonical legacy asset a3 is missing or not USDT';
    END IF;

    INSERT INTO asset_identity_merge_audit(
        provider, external_id, from_asset_guid, to_asset_guid, reason
    )
    VALUES (
        'coingecko', 'tether', duplicate_asset_guid, 'a3',
        'top50 manifest explicitly reviews legacy a3 as canonical USDT'
    )
    ON CONFLICT DO NOTHING;

    UPDATE asset_alias
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    UPDATE provider_market_candidate
    SET base_asset_guid = CASE WHEN base_asset_guid = duplicate_asset_guid THEN 'a3' ELSE base_asset_guid END,
        quote_asset_guid = CASE WHEN quote_asset_guid = duplicate_asset_guid THEN 'a3' ELSE quote_asset_guid END
    WHERE base_asset_guid = duplicate_asset_guid
       OR quote_asset_guid = duplicate_asset_guid;

    UPDATE symbol
    SET base_asset_guid = CASE WHEN base_asset_guid = duplicate_asset_guid THEN 'a3' ELSE base_asset_guid END,
        qoute_asset_guid = CASE WHEN qoute_asset_guid = duplicate_asset_guid THEN 'a3' ELSE qoute_asset_guid END,
        updated_at = clock_timestamp()
    WHERE base_asset_guid = duplicate_asset_guid
       OR qoute_asset_guid = duplicate_asset_guid;

    UPDATE asset_representation
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM asset_metric_current WHERE asset_guid = 'a3';
    UPDATE asset_metric_current
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM asset_price_index WHERE asset_guid = 'a3';
    UPDATE asset_price_index
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM asset_venue_snapshot target
    USING asset_venue_snapshot source
    WHERE source.asset_guid = duplicate_asset_guid
      AND target.asset_guid = 'a3'
      AND target.provider = source.provider
      AND target.price_kind = source.price_kind;
    UPDATE asset_venue_snapshot
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM dex_route_current target
    USING dex_route_current source
    WHERE source.asset_guid = duplicate_asset_guid
      AND target.asset_guid = 'a3'
      AND target.provider = source.provider
      AND target.route_key = source.route_key;
    UPDATE dex_route_current
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM dex_quote_observation target
    USING dex_quote_observation source
    WHERE source.asset_guid = duplicate_asset_guid
      AND target.asset_guid = 'a3'
      AND target.provider = source.provider
      AND target.route_key = source.route_key
      AND target.observed_at = source.observed_at;
    UPDATE dex_quote_observation
    SET asset_guid = 'a3'
    WHERE asset_guid = duplicate_asset_guid;

    DELETE FROM asset_external_mapping
    WHERE asset_guid = duplicate_asset_guid
      AND provider <> 'coingecko';
    DELETE FROM asset_external_mapping
    WHERE provider = 'coingecko'
      AND asset_guid = 'a3';
    UPDATE asset_external_mapping
    SET asset_guid = 'a3', updated_at = clock_timestamp()
    WHERE provider = 'coingecko'
      AND external_id = 'tether'
      AND asset_guid = duplicate_asset_guid;

    DELETE FROM asset WHERE guid = duplicate_asset_guid;
END $$;

COMMIT;
