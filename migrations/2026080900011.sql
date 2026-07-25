-- Provider rollout evidence and operator-visible retry state.
--
-- Counters are scoped to the current rollout observation window. The rollout
-- command resets them whenever it changes provider mode/configuration, so a
-- promotion cannot reuse stale successes from an earlier canary.

BEGIN;

ALTER TABLE market_provider_status
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS window_started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN IF NOT EXISTS attempt_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS success_count BIGINT NOT NULL DEFAULT 0;

ALTER TABLE market_provider_status
    DROP CONSTRAINT IF EXISTS market_provider_status_attempt_count_nonnegative,
    DROP CONSTRAINT IF EXISTS market_provider_status_success_count_nonnegative;

ALTER TABLE market_provider_status
    ADD CONSTRAINT market_provider_status_attempt_count_nonnegative
        CHECK (attempt_count >= 0),
    ADD CONSTRAINT market_provider_status_success_count_nonnegative
        CHECK (success_count >= 0);

UPDATE market_provider_status
SET window_started_at = COALESCE(last_success_at, last_attempt_at, updated_at, clock_timestamp())
WHERE window_started_at IS NULL;

COMMIT;
