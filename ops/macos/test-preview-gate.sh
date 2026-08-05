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
export QIU_MARKET_PREVIEW_OAUTH_WINDOW_REPORT="$fixture_dir/window-close.json"
export QIU_MARKET_ENV_FILE="$fixture_dir/production.env"
export QIU_MARKET_VERCEL_PROJECT_FILE="$fixture_dir/vercel-project.json"
printf '{"projectId":"prj_fixture","orgId":"team_fixture"}\n' \
  > "$QIU_MARKET_VERCEL_PROJECT_FILE"
verifier="$repo_root/ops/macos/verify-preview-gate.sh"
deployment_id="dpl_FixturePreview123"
deployment_url="https://qiu-market-fixture-preview.vercel.app"
deployment_commit="$(git -C "$repo_root" rev-parse HEAD)"
export FIXTURE_DEPLOYMENT_COMMIT="$deployment_commit"
export FIXTURE_TRANSIENT_ENDPOINT="/api/v1/trading/session"
export FIXTURE_TRANSIENT_STATE="$fixture_dir/session-transient-once"

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
test -f "$FIXTURE_TRANSIENT_STATE"
unset FIXTURE_TRANSIENT_ENDPOINT FIXTURE_TRANSIENT_STATE

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
  .reason == "managed_oauth_close_evidence_missing" and
  .checks.oauth_private_configuration_present == true and
  .checks.github_oauth_runtime_capability == true and
  .checks.managed_oauth_close_evidence_verified == false and
  .checks.oauth_browser_evidence_verified == false
' "$fixture_dir/report.json" >/dev/null

jq -n \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" '{
    schema_version: 1,
    status: "closed_after_verified_logout",
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    window_id: "0123456789abcdef0123456789abcdef",
    window_opened_at: "2026-07-28T00:00:00Z",
    production_configuration_restored: true,
    production_oauth_runtime_verified: true,
    completed_at: "2026-07-28T00:10:00Z"
  }' > "$fixture_dir/window-close.json"
chmod 600 "$fixture_dir/window-close.json"
if run_verifier; then
  echo "Preview gate incorrectly passed without final browser evidence." >&2
  exit 1
else
  verifier_code=$?
fi
if [ "$verifier_code" != 2 ]; then
  echo "Missing final browser evidence did not return environment-pending." >&2
  exit 1
fi
jq -e '
  .status == "environment-pending" and
  .reason == "oauth_browser_evidence_missing" and
  .checks.managed_oauth_close_evidence_verified == true and
  .checks.oauth_browser_evidence_verified == false
' "$fixture_dir/report.json" >/dev/null

jq -n \
  --arg deployment_id "$deployment_id" \
  --arg deployment_commit "$deployment_commit" '{
    schema_version: 2,
    deployment_id: $deployment_id,
    deployment_commit: $deployment_commit,
    window_id: "0123456789abcdef0123456789abcdef",
    window_opened_at: "2026-07-28T00:00:00Z",
    maintenance_closed_at: "2026-07-28T00:10:00Z",
    callback_single_use: true,
    secure_cookie: true,
    csrf_rejected: true,
    origin_rejected: true,
    submit_unknown_reconciled: true,
    cancel_unknown_reconciled: true,
    fund_unknown_reconciled: true,
    preview_logout_204: true,
    stale_preview_session_401: true,
    stale_preview_write_401: true,
    visual_trade_page: true,
    console_error_count: 0,
    completed_at: "2026-07-28T00:11:00.123Z"
  }' > "$fixture_dir/oauth-evidence.json"
chmod 600 "$fixture_dir/oauth-evidence.json"
run_verifier
jq -e '
  .status == "preview-gate-passed" and
  .reason == "all_preview_security_evidence_verified" and
  .checks.managed_oauth_close_evidence_verified == true and
  .checks.oauth_browser_evidence_verified == true
' "$fixture_dir/report.json" >/dev/null

jq '.window_id = "ffffffffffffffffffffffffffffffff"' \
  "$fixture_dir/oauth-evidence.json" > "$fixture_dir/oauth-evidence-mismatched.json"
mv "$fixture_dir/oauth-evidence-mismatched.json" "$fixture_dir/oauth-evidence.json"
chmod 600 "$fixture_dir/oauth-evidence.json"
if run_verifier; then
  echo "Preview gate incorrectly accepted evidence from another OAuth window." >&2
  exit 1
else
  verifier_code=$?
fi
if [ "$verifier_code" != 2 ]; then
  echo "Mismatched OAuth window evidence did not remain environment-pending." >&2
  exit 1
fi
jq -e '
  .status == "environment-pending" and
  .reason == "oauth_browser_evidence_missing" and
  .checks.managed_oauth_close_evidence_verified == true and
  .checks.oauth_browser_evidence_verified == false
' "$fixture_dir/report.json" >/dev/null

jq '.window_id = "0123456789abcdef0123456789abcdef"' \
  "$fixture_dir/oauth-evidence.json" > "$fixture_dir/oauth-evidence-restored.json"
mv "$fixture_dir/oauth-evidence-restored.json" "$fixture_dir/oauth-evidence.json"
chmod 600 "$fixture_dir/oauth-evidence.json"

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

if ! grep -Fq -- '--silent --show-error --max-time 20' \
  "$repo_root/ops/macos/verify-preview-gate.sh"; then
  echo "Preview gate protected reads do not have a bounded transport timeout." >&2
  exit 1
fi

echo "Qiu Market protected Preview gate fixtures passed."
