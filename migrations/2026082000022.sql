-- Preserve the AMM protocol used by every hop of an indicative route.
--
-- A route may now combine native V2 and V3 pools.  The token path and pool
-- addresses are insufficient to explain which read-only quote contract was
-- used, so the ordered protocol list is stored beside them.

BEGIN;

ALTER TABLE dex_route_current
    ADD COLUMN IF NOT EXISTS protocol_versions JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE dex_route_current
    DROP CONSTRAINT IF EXISTS dex_route_current_protocol_versions_check;

ALTER TABLE dex_route_current
    ADD CONSTRAINT dex_route_current_protocol_versions_check
    CHECK (jsonb_typeof(protocol_versions) = 'array');

COMMIT;
