-- Bind every writable recovery epoch to one trusted public origin and one
-- immutable release. Operator-supplied endpoints cannot establish this
-- identity; the trading service persists it when the epoch begins.

ALTER TABLE trading_recovery_epoch
    ADD COLUMN IF NOT EXISTS production_origin TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS release_commit TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_digest TEXT NOT NULL DEFAULT '';

-- No pre-migration writable epoch has a trusted provenance binding. Keep its
-- audit history, but fail it closed rather than grandfathering it.
UPDATE trading_recovery_epoch
SET phase = 'manual_review',
    writes_enabled = FALSE,
    transport_healthy = FALSE,
    last_error = CASE
        WHEN last_error = '' THEN 'legacy writable epoch lacks production provenance'
        ELSE last_error
    END,
    updated_at = now()
WHERE phase = 'writable'
  AND (
      production_origin = '' OR deployment_id = '' OR deployment_url = '' OR
      release_commit = '' OR source_digest = ''
  );

ALTER TABLE trading_recovery_epoch
    DROP CONSTRAINT IF EXISTS trading_recovery_provenance_check;

ALTER TABLE trading_recovery_epoch
    ADD CONSTRAINT trading_recovery_provenance_check CHECK (
        phase <> 'writable' OR (
            production_origin ~ '^https://[^/]+$'
            AND deployment_id ~ '^dpl_[A-Za-z0-9]{8,128}$'
            AND deployment_url ~ '^https://[^/]+[.]vercel[.]app$'
            AND deployment_url <> production_origin
            AND release_commit ~ '^[0-9a-f]{40}$'
            AND source_digest ~ '^[0-9a-f]{64}$'
        )
    );
