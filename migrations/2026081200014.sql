-- Provider feed evidence and the DW continuous reconciliation safety gate.
--
-- details stores bounded, non-sensitive counters for the latest successful
-- provider observation. It must never contain endpoint URLs, credentials, or
-- raw upstream payloads.

BEGIN;

ALTER TABLE market_provider_status
    ADD COLUMN IF NOT EXISTS details JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS dw_acceptance_state (
    stream_name TEXT PRIMARY KEY,
    continuous_success_started_at TIMESTAMPTZ,
    last_attempt_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    successful_cycles BIGINT NOT NULL DEFAULT 0,
    last_error_summary TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT dw_acceptance_state_failures_nonnegative
        CHECK (consecutive_failures >= 0),
    CONSTRAINT dw_acceptance_state_cycles_nonnegative
        CHECK (successful_cycles >= 0)
);

COMMIT;
