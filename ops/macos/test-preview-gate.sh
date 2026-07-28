#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_bin="$repo_root/ops/macos/fixtures/preview-gate-bin"
fixture_dir="$(mktemp -d /tmp/qiu-market-preview-gate.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

export PATH="$fixture_bin:$PATH"
export QIU_MARKET_FUNNEL_ORIGIN="https://fixture-funnel.invalid"
export QIU_MARKET_PREVIEW_GATE_REPORT="$fixture_dir/report.json"
export QIU_MARKET_PREVIEW_OAUTH_EVIDENCE_FILE="$fixture_dir/oauth-evidence.json"
export QIU_MARKET_ENV_FILE="$fixture_dir/production.env"
verifier="$repo_root/ops/macos/verify-preview-gate.sh"
deployment_id="dpl_FixturePreview123"
deployment_url="https://qiu-market-fixture-preview.vercel.app"
deployment_commit="2aa8bda39d2298e1d57886e472f9a090d728f56e"

run_verifier() {
  "$verifier" \
    --deployment-id "$deployment_id" \
    --deployment-url "$deployment_url" \
    --commit "$deployment_commit" >/dev/null
}

if run_verifier; then
  echo "Preview gate incorrectly passed without OAuth configuration." >&2
  exit 1
else
  verifier_code=$?
fi
if [ "$verifier_code" != 2 ]; then
  echo "Missing OAuth configuration did not return environment-pending." >&2
  exit 1
fi
jq -e '
  .status == "environment-pending" and
  .reason == "github_oauth_private_configuration_missing" and
  .checks.inspect_ready_preview == true and
  .checks.local_frontend_matches_release == true and
  .checks.vercel_authentication_blocks_ordinary_access == true and
  .checks.protected_spa_deep_links_200 == true and
  .checks.runtime_provenance_matches == true and
  .checks.trading_and_outbox_ready == true and
  .checks.anonymous_session_http == 401 and
  .checks.unsigned_funnel_rest_http == 401 and
  .checks.local_login_disabled == true
' "$fixture_dir/report.json" >/dev/null

printf '%s\n' \
  'MARKET_TRADING_GITHUB_CLIENT_ID=fixture-client-id' \
  'MARKET_TRADING_GITHUB_CLIENT_SECRET=fixture-client-secret' \
  > "$fixture_dir/production.env"
chmod 600 "$fixture_dir/production.env"
export FIXTURE_GITHUB_OAUTH_ENABLED=1
if run_verifier; then
  echo "Preview gate incorrectly passed without browser evidence." >&2
  exit 1
else
  verifier_code=$?
fi
if [ "$verifier_code" != 2 ]; then
  echo "Missing browser evidence did not return environment-pending." >&2
  exit 1
fi
jq -e '
  .status == "environment-pending" and
  .reason == "oauth_browser_evidence_missing" and
  .checks.oauth_private_configuration_present == true and
  .checks.github_oauth_runtime_capability == true and
  .checks.oauth_browser_evidence_verified == false
' "$fixture_dir/report.json" >/dev/null

jq -n \
  --arg deployment_id "$deployment_id" \
  --arg deployment_commit "$deployment_commit" '{
    schema_version: 1,
    deployment_id: $deployment_id,
    deployment_commit: $deployment_commit,
    callback_single_use: true,
    secure_cookie: true,
    csrf_rejected: true,
    origin_rejected: true,
    submit_unknown_reconciled: true,
    cancel_unknown_reconciled: true,
    fund_unknown_reconciled: true,
    preview_logout_204: true,
    stale_preview_session_401: true,
    completed_at: "2026-07-28T00:00:00Z"
  }' > "$fixture_dir/oauth-evidence.json"
chmod 600 "$fixture_dir/oauth-evidence.json"
run_verifier
jq -e '
  .status == "preview-gate-passed" and
  .reason == "all_preview_security_evidence_verified" and
  .checks.oauth_browser_evidence_verified == true
' "$fixture_dir/report.json" >/dev/null

export FIXTURE_BAD_PROVENANCE=1
if run_verifier; then
  echo "Preview gate incorrectly accepted mismatched runtime provenance." >&2
  exit 1
else
  verifier_code=$?
fi
if [ "$verifier_code" != 1 ]; then
  echo "Mismatched runtime provenance did not fail the Preview gate." >&2
  exit 1
fi
jq -e '
  .status == "failed" and
  .reason == "non_oauth_preview_check_failed" and
  .checks.runtime_provenance_matches == false
' "$fixture_dir/report.json" >/dev/null

echo "Qiu Market protected Preview gate fixtures passed."
