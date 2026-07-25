BEGIN;

-- CoinGecko platform contracts are admitted only after the canonical
-- CoinGecko asset id has already been mapped and the token contract has
-- answered an on-chain decimals() call. They are useful provider-declared
-- representations, but are not necessarily the canonical issuance contract.
ALTER TABLE asset_representation
    DROP CONSTRAINT IF EXISTS asset_representation_representation_kind_check;

ALTER TABLE asset_representation
    ADD CONSTRAINT asset_representation_representation_kind_check
    CHECK (
        representation_kind IN (
            'native_wrapped',
            'canonical',
            'wrapped',
            'bridged',
            'provider_mapped'
        )
    );

-- Pool identity and quote eligibility are separate facts. A real V3 pool can
-- make an asset part of the provider's Top-50 catalog even when liquidity,
-- volume, block freshness, or a 10K round trip is not good enough to publish
-- an indicative quote.
ALTER TABLE dex_pool_candidate
    ADD COLUMN IF NOT EXISTS quote_eligible BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS dex_pool_candidate_asset_lookup_idx
    ON dex_pool_candidate(
        provider,
        chain_id,
        resolution_status,
        token0_address,
        token1_address
    );

COMMIT;
