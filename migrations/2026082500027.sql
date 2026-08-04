-- Persist only the bounded, content-addressed transport observation summary.
-- Raw HTTP/gRPC responses remain operator-local evidence and are not trading
-- truth. The CHECK constraints prevent a writable epoch without the minimum
-- continuous sample window required by the Recovery Coordinator.

ALTER TABLE trading_recovery_epoch
    ADD COLUMN IF NOT EXISTS transport_sample_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transport_first_sample_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS transport_last_sample_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS transport_maximum_gap_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transport_evidence_sha256 TEXT NOT NULL DEFAULT '';

-- Older development epochs could reach writable through the generic Advance
-- API and therefore have no bound transport evidence. They are closed instead
-- of being grandfathered into the stronger admission rule.
UPDATE trading_recovery_epoch
SET phase = 'manual_review',
    writes_enabled = FALSE,
    transport_healthy = FALSE,
    last_error = CASE
        WHEN last_error = '' THEN 'legacy writable epoch lacks bound transport evidence'
        ELSE last_error
    END,
    updated_at = now()
WHERE phase = 'writable'
  AND (
      transport_sample_count < 7
      OR transport_first_sample_at IS NULL
      OR transport_last_sample_at IS NULL
      OR transport_evidence_sha256 = ''
  );

ALTER TABLE trading_recovery_epoch
    DROP CONSTRAINT IF EXISTS trading_recovery_transport_evidence_check;

ALTER TABLE trading_recovery_epoch
    ADD CONSTRAINT trading_recovery_transport_evidence_check CHECK (
        phase <> 'writable' OR (
            transport_healthy
            AND transport_sample_count >= 7
            AND transport_first_sample_at IS NOT NULL
            AND transport_last_sample_at IS NOT NULL
            AND transport_last_sample_at - transport_first_sample_at >= INTERVAL '30 seconds'
            AND transport_maximum_gap_ms BETWEEN 0 AND 8000
            AND length(transport_evidence_sha256) = 64
        )
    );
