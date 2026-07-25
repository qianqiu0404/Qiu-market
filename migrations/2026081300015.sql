-- Provider readiness counters must be internally consistent.
--
-- Migration 2026081200014 introduced a NOT NULL details object. During the
-- first live verification, an application insert path explicitly supplied
-- NULL for attempts while the paired success insert supplied '{}'. Every
-- recorded success proves at least one upstream request, so raising attempts
-- to the success count is a conservative repair; it does not invent successes
-- or advance an observation timestamp.

BEGIN;

UPDATE market_provider_status
SET attempt_count = success_count,
    updated_at = clock_timestamp()
WHERE attempt_count < success_count;

ALTER TABLE market_provider_status
    DROP CONSTRAINT IF EXISTS market_provider_status_attempts_cover_successes;

ALTER TABLE market_provider_status
    ADD CONSTRAINT market_provider_status_attempts_cover_successes
    CHECK (attempt_count >= success_count);

COMMIT;
