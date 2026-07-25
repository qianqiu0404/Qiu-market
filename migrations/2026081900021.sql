-- Versioned CEX K-line market selection.
--
-- provider_asset_selection freezes the 50 assets for each venue. This table
-- records the one concrete USD-family spot market chosen for each selected
-- asset, so K-line membership is auditable and does not drift with ticker
-- catalog ordering.

BEGIN;

CREATE TABLE IF NOT EXISTS provider_kline_selection (
    provider          TEXT        NOT NULL,
    selection_version BIGINT      NOT NULL,
    asset_guid        TEXT        NOT NULL REFERENCES asset(guid),
    market_id         TEXT        NOT NULL REFERENCES exchange_symbol(guid),
    source_symbol     TEXT        NOT NULL,
    quote_asset_guid  TEXT        NOT NULL REFERENCES asset(guid),
    selection_rank    INTEGER     NOT NULL,
    selection_reason  TEXT        NOT NULL,
    selected_at       TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, selection_version, asset_guid),
    UNIQUE (provider, selection_version, market_id),
    UNIQUE (provider, selection_version, selection_rank),
    CHECK (provider IN ('binance', 'coinbase', 'bybit', 'okx')),
    CHECK (selection_version > 0),
    CHECK (selection_rank > 0),
    CHECK (length(btrim(source_symbol)) > 0)
);

CREATE INDEX IF NOT EXISTS provider_kline_selection_market_idx
    ON provider_kline_selection(market_id, provider, selection_version);

COMMIT;
