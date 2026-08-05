-- Extend the stable, versioned provider asset universe to the three DEX
-- adapters.  A DEX selection may contain fewer than its target because only
-- reviewed, technically valid markets/routes are allowed to appear; the
-- target is a ceiling, never a request to pad the page with fake assets.

BEGIN;

ALTER TABLE provider_asset_selection_state
    DROP CONSTRAINT IF EXISTS provider_asset_selection_state_provider_check;

ALTER TABLE provider_asset_selection_state
    ADD CONSTRAINT provider_asset_selection_state_provider_check
    CHECK (
        provider IN (
            'binance', 'coinbase', 'bybit', 'okx',
            'hyperliquid', 'uniswap', 'pancakeswap'
        )
    );

ALTER TABLE provider_asset_selection
    DROP CONSTRAINT IF EXISTS provider_asset_selection_provider_check;

ALTER TABLE provider_asset_selection
    ADD CONSTRAINT provider_asset_selection_provider_check
    CHECK (
        provider IN (
            'binance', 'coinbase', 'bybit', 'okx',
            'hyperliquid', 'uniswap', 'pancakeswap'
        )
    );

COMMIT;
