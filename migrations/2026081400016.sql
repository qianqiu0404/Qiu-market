-- Local CEX preview is an operator-only development mode. It publishes
-- reviewed Top-50 spot markets without mutating the formal rollout mode,
-- canary list, soak window, or readiness evidence.

BEGIN;

ALTER TABLE provider_rollout_state
    ADD COLUMN IF NOT EXISTS local_preview_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS local_preview_enabled_at TIMESTAMPTZ;

ALTER TABLE provider_rollout_state
    DROP CONSTRAINT IF EXISTS provider_rollout_preview_timestamp_check;

ALTER TABLE provider_rollout_state
    ADD CONSTRAINT provider_rollout_preview_timestamp_check
    CHECK (
        (local_preview_enabled AND local_preview_enabled_at IS NOT NULL)
        OR
        (NOT local_preview_enabled AND local_preview_enabled_at IS NULL)
    );

COMMIT;
