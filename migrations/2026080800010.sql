-- Top 50 ticker rollout must not silently expand the K-line product.
--
-- The first Binance canary exposed a boundary bug: the worker scanned every
-- enabled ticker market, created repair tasks for 13 markets outside the
-- reviewed six-market K-line scope, and the crawler faithfully backfilled
-- them. This migration makes K-line membership explicit and removes only the
-- audited accidental rows/tasks for Binance markets outside that scope.

BEGIN;

ALTER TABLE exchange_symbol
    ADD COLUMN IF NOT EXISTS kline_enabled BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE exchange_symbol
SET kline_enabled = TRUE,
    updated_at = clock_timestamp()
WHERE market_code IN (
    'binance:BTC/USDT:spot',
    'binance:ETH/USDT:spot',
    'binance:SOL/USDT:spot',
    'binance:BNB/USDT:spot',
    'binance:XRP/USDT:spot',
    'binance:DOGE/USDT:spot'
)
  AND kline_enabled IS DISTINCT FROM TRUE;

CREATE TABLE IF NOT EXISTS kline_scope_cleanup_audit (
    cleanup_key          TEXT        PRIMARY KEY,
    removed_market_count BIGINT      NOT NULL,
    removed_kline_count  BIGINT      NOT NULL,
    removed_task_count   BIGINT      NOT NULL,
    removed_watermark_count BIGINT   NOT NULL DEFAULT 0,
    reason               TEXT        NOT NULL,
    cleaned_at           TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (removed_market_count >= 0),
    CHECK (removed_kline_count >= 0),
    CHECK (removed_task_count >= 0),
    CHECK (removed_watermark_count >= 0)
);

ALTER TABLE kline_scope_cleanup_audit
    ADD COLUMN IF NOT EXISTS removed_watermark_count BIGINT NOT NULL DEFAULT 0;

DO $$
DECLARE
    affected_market_count BIGINT;
    affected_kline_count  BIGINT;
    affected_task_count   BIGINT;
    affected_watermark_count BIGINT;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM kline_scope_cleanup_audit
        WHERE cleanup_key = 'top50-ticker-only-20260724'
    ) THEN
        RETURN;
    END IF;

    SELECT count(DISTINCT es.guid)
    INTO affected_market_count
    FROM exchange_symbol es
    JOIN exchange provider_exchange
      ON provider_exchange.guid = es.exchange_guid
    WHERE provider_exchange.code = 'binance'
      AND es.kline_enabled = FALSE
      AND (
          EXISTS (
              SELECT 1
              FROM symbol_kline kline
              WHERE kline.market_id = es.guid
          )
          OR EXISTS (
              SELECT 1
              FROM kline_repair_task task
              WHERE task.market_id = es.guid
          )
      );

    SELECT count(*)
    INTO affected_kline_count
    FROM symbol_kline kline
    JOIN exchange_symbol es
      ON es.guid = kline.market_id
    JOIN exchange provider_exchange
      ON provider_exchange.guid = es.exchange_guid
    WHERE provider_exchange.code = 'binance'
      AND es.kline_enabled = FALSE;

    SELECT count(*)
    INTO affected_task_count
    FROM kline_repair_task task
    JOIN exchange_symbol es
      ON es.guid = task.market_id
    JOIN exchange provider_exchange
      ON provider_exchange.guid = es.exchange_guid
    WHERE provider_exchange.code = 'binance'
      AND es.kline_enabled = FALSE;

    SELECT count(*)
    INTO affected_watermark_count
    FROM dw_sync_state state
    WHERE EXISTS (
        SELECT 1
        FROM exchange_symbol es
        JOIN exchange provider_exchange
          ON provider_exchange.guid = es.exchange_guid
        WHERE provider_exchange.code = 'binance'
          AND es.kline_enabled = FALSE
          AND (
              state.stream_name LIKE 'kline-v2:' || es.guid || ':%'
              OR state.stream_name LIKE 'kline:' || es.symbol_guid || ':%'
          )
    );

    DELETE FROM dw_sync_state state
    WHERE EXISTS (
        SELECT 1
        FROM exchange_symbol es
        JOIN exchange provider_exchange
          ON provider_exchange.guid = es.exchange_guid
        WHERE provider_exchange.code = 'binance'
          AND es.kline_enabled = FALSE
          AND (
              state.stream_name LIKE 'kline-v2:' || es.guid || ':%'
              OR state.stream_name LIKE 'kline:' || es.symbol_guid || ':%'
          )
    );

    DELETE FROM kline_repair_task task
    USING exchange_symbol es, exchange provider_exchange
    WHERE task.market_id = es.guid
      AND provider_exchange.guid = es.exchange_guid
      AND provider_exchange.code = 'binance'
      AND es.kline_enabled = FALSE;

    DELETE FROM symbol_kline kline
    USING exchange_symbol es, exchange provider_exchange
    WHERE kline.market_id = es.guid
      AND provider_exchange.guid = es.exchange_guid
      AND provider_exchange.code = 'binance'
      AND es.kline_enabled = FALSE;

    INSERT INTO kline_scope_cleanup_audit(
        cleanup_key,
        removed_market_count,
        removed_kline_count,
        removed_task_count,
        removed_watermark_count,
        reason
    )
    VALUES (
        'top50-ticker-only-20260724',
        affected_market_count,
        affected_kline_count,
        affected_task_count,
        affected_watermark_count,
        'Top 50 ticker canary must not expand the reviewed six-market Binance K-line product'
    );
END $$;

COMMIT;
