-- Rollout soak must start when the provider feed is actually observed, not
-- when an operator runs the rollout command. A provider that starts several
-- hours late must still complete the full canary/enabled observation window.

BEGIN;

ALTER TABLE market_provider_status
    ADD COLUMN IF NOT EXISTS observation_started_at TIMESTAMPTZ;

-- Existing rollout counters were created before the actual-observation
-- boundary existed. Reset only the feed used by the promotion gate so an old
-- command timestamp cannot be reused as evidence. The next real attempt starts
-- a fresh observation window.
UPDATE market_provider_status status
SET observation_started_at = NULL,
    attempt_count = 0,
    success_count = 0,
    next_retry_at = NULL,
    updated_at = clock_timestamp()
FROM provider_rollout_state rollout
WHERE rollout.provider = status.provider
  AND status.source_key = CASE
      WHEN status.provider = 'hyperliquid' THEN 'metaAndAssetCtxs'
      WHEN status.provider IN ('uniswap', 'pancakeswap') THEN 'route-quotes'
      ELSE 'spot-tickers'
  END;

COMMIT;
