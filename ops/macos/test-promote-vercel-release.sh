#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_bin="$repo_root/ops/macos/fixtures/vercel-promotion-bin"
fixture_dir="$(mktemp -d /tmp/qiu-market-vercel-promotion.XXXXXX)"
cleanup() {
  if [ "${QIU_MARKET_KEEP_PROMOTION_FIXTURE:-false}" = true ]; then
    printf 'Promotion fixture retained at %s\n' "$fixture_dir" >&2
    return
  fi
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

support_dir="$fixture_dir/support"
release_dir="$support_dir/vercel-release"
observations="$support_dir/observations"
mkdir -p "$observations"
chmod 700 "$support_dir" "$observations"

candidate_id="dpl_PromotionFixture123"
candidate_host="qiu-market-promotion-fixture.vercel.app"
candidate_url="https://$candidate_host"
promoted_id="dpl_PromotedFixture123"
promoted_host="qiu-market-promoted-fixture.vercel.app"
candidate_commit="$(git -C "$repo_root" rev-parse HEAD)"
previous_id="dpl_PreviousFixture123"
previous_host="qiu-market-previous-fixture.vercel.app"
vercel_state="$fixture_dir/vercel-state.json"
gate_report="$observations/preview-gate-latest.json"
production_evidence="$observations/production-auth-evidence.json"

export PATH="$fixture_bin:$PATH"
export QIU_MARKET_SUPPORT_DIR="$support_dir"
export QIU_MARKET_VERCEL_RELEASE_DIR="$release_dir"
export QIU_MARKET_PREVIEW_GATE_REPORT="$gate_report"
export QIU_MARKET_PRODUCTION_AUTH_EVIDENCE_FILE="$production_evidence"
export QIU_MARKET_FIXTURE_VERCEL_STATE="$vercel_state"
export QIU_MARKET_FIXTURE_CANDIDATE_ID="$candidate_id"
export QIU_MARKET_FIXTURE_CANDIDATE_HOST="$candidate_host"
export QIU_MARKET_FIXTURE_PROMOTED_ID="$promoted_id"
export QIU_MARKET_FIXTURE_PROMOTED_HOST="$promoted_host"
export QIU_MARKET_FIXTURE_PREVIOUS_ID="$previous_id"
export QIU_MARKET_FIXTURE_PREVIOUS_HOST="$previous_host"
export QIU_MARKET_FIXTURE_COMMIT="$candidate_commit"
export QIU_MARKET_FIXTURE_PROJECT_ID="$(
  jq -r '.projectId' "$repo_root/frontend/.vercel/project.json"
)"
export QIU_MARKET_PRODUCTION_ORIGIN="https://qiu-market.vercel.app"
export QIU_MARKET_FUNNEL_ORIGIN="https://fixture-funnel.invalid"
export QIU_MARKET_PROMOTION_SMOKE_ATTEMPTS=1
export QIU_MARKET_PROMOTION_SMOKE_INTERVAL_SECONDS=0
export QIU_MARKET_PROMOTION_RESOLVE_ATTEMPTS=3
export QIU_MARKET_PROMOTION_RESOLVE_INTERVAL_SECONDS=0

manager="$repo_root/ops/macos/promote-vercel-release.sh"
identity_args=(
  --deployment-id "$candidate_id"
  --deployment-url "$candidate_url"
  --commit "$candidate_commit"
)

reset_vercel() {
  rm -f "$vercel_state.project-api-count"
  jq -n \
    --arg id "$previous_id" \
    --arg host "$previous_host" \
    '{production_id:$id,production_host:$host}' > "$vercel_state"
}

write_gate() {
  jq -n \
    --arg checked_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --arg deployment_id "$candidate_id" \
    --arg deployment_url "$candidate_url" \
    --arg deployment_commit "$candidate_commit" '{
      schema_version: 1,
      checked_at: $checked_at,
      status: "preview-gate-passed",
      reason: "all_preview_security_evidence_verified",
      deployment: {
        id: $deployment_id,
        url: $deployment_url,
        commit: $deployment_commit
      },
      checks: {
        inspect_ready_preview: true,
        local_frontend_matches_release: true,
        vercel_authentication_blocks_ordinary_access: true,
        protected_spa_deep_links_200: true,
        runtime_provenance_matches: true,
        trading_and_outbox_ready: true,
        anonymous_session_http: 401,
        unsigned_funnel_rest_http: 401,
        local_login_disabled: true,
        oauth_private_configuration_present: true,
        github_oauth_runtime_capability: true,
        managed_oauth_close_evidence_verified: true,
        oauth_browser_evidence_verified: true
      }
    }' > "$gate_report"
  chmod 600 "$gate_report"
}

reset_vercel
write_gate

jq '.checked_at = "2000-01-01T00:00:00Z"' \
  "$gate_report" > "$gate_report.stale"
mv "$gate_report.stale" "$gate_report"
chmod 600 "$gate_report"
if "$manager" preflight "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Promotion preflight accepted stale Preview Gate evidence." >&2
  exit 1
fi
write_gate

mkdir -p "$support_dir/preview-oauth-window"
printf '{"phase":"open"}\n' > "$support_dir/preview-oauth-window/active.json"
if "$manager" preflight "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Promotion preflight ignored an active OAuth maintenance window." >&2
  exit 1
fi
rm -f "$support_dir/preview-oauth-window/active.json"

printf '{"status":"active"}\n' > "$observations/acceptance-epoch.json"
if "$manager" preflight "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Promotion preflight ignored an active acceptance epoch." >&2
  exit 1
fi
rm -f "$observations/acceptance-epoch.json"

"$manager" preflight "${identity_args[@]}" > "$fixture_dir/preflight.json"
jq -e '
  .status == "promotion-preflight-passed" and
  .mutated == false
' "$fixture_dir/preflight.json" >/dev/null
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]

if "$manager" promote "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Promotion unexpectedly succeeded without --execute." >&2
  exit 1
fi

"$manager" promote --execute "${identity_args[@]}" > "$fixture_dir/promoted.json"
jq -e '
  .phase == "awaiting-production-auth" and
  ((.promotion_id // "") | test("^[0-9a-f]{32}$"))
' "$fixture_dir/promoted.json" >/dev/null
[ "$(jq -r '.production_id' "$vercel_state")" = "$promoted_id" ]

if "$manager" confirm >/dev/null 2>&1; then
  echo "Production confirmation unexpectedly accepted missing evidence." >&2
  exit 1
fi
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]
jq -e '
  .status == "rolled_back_after_production_evidence_failure" and
  .rollback_verified == true
' "$release_dir/last.json" >/dev/null

reset_vercel
write_gate
export QIU_MARKET_FIXTURE_STALE_PROMOTED_FIRST=1
"$manager" promote --execute "${identity_args[@]}" > "$fixture_dir/promoted-after-stale.json"
unset QIU_MARKET_FIXTURE_STALE_PROMOTED_FIRST
jq -e \
  --arg promoted_id "$promoted_id" '
    .phase == "awaiting-production-auth" and
    .promoted.id == $promoted_id
  ' "$fixture_dir/promoted-after-stale.json" >/dev/null
"$manager" rollback >/dev/null
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]

reset_vercel
write_gate
"$manager" promote --execute "${identity_args[@]}" >/dev/null
promotion_id="$(jq -r '.promotion_id' "$release_dir/active.json")"
promoted_at="$(jq -r '.promoted_at' "$release_dir/active.json")"
jq -n \
  --arg promotion_id "$promotion_id" \
  --arg deployment_id "$candidate_id" \
  --arg production_deployment_id "$promoted_id" \
  --arg deployment_commit "$candidate_commit" \
  --arg completed_at "$promoted_at" '{
    schema_version: 1,
    promotion_id: $promotion_id,
    deployment_id: $deployment_id,
    production_deployment_id: $production_deployment_id,
    deployment_commit: $deployment_commit,
    production_login: true,
    github_login: "qianqiu0404",
    secure_cookie: true,
    csrf_rejected: true,
    origin_rejected: true,
    minimal_virtual_write_reconciled: true,
    request_id: "prod-fixture-request-001",
    same_request_id_replay_equal: true,
    ledger_balanced: true,
    state_hash_consistent: true,
    production_logout_204: true,
    stale_production_session_401: true,
    completed_at: $completed_at
  }' > "$production_evidence"
chmod 600 "$production_evidence"
"$manager" confirm > "$fixture_dir/confirmed.json"
jq -e '
  .status == "production-gate-passed" and
  .rollback_verified == false
' "$fixture_dir/confirmed.json" >/dev/null
[ ! -e "$release_dir/active.json" ]

if ! grep -Fq 'for attempt in 1 2 3' "$manager" ||
  ! grep -Fq '000|5??)' "$manager" ||
  ! grep -Fq -- '--silent --show-error --max-time 20' "$manager" ||
  ! grep -Fq 'production_request \' "$manager" ||
  ! grep -Fq 'baseline-oauth-start \' "$manager"; then
  echo "Promotion preflight candidate reads are not bounded and retry-safe." >&2
  exit 1
fi

reset_vercel
write_gate
export QIU_MARKET_FIXTURE_PRODUCTION_FAIL=1
if "$manager" promote --execute "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Structurally failed Production promotion unexpectedly succeeded." >&2
  exit 1
fi
unset QIU_MARKET_FIXTURE_PRODUCTION_FAIL
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]
jq -e '
  .status == "rolled_back_after_failed_promotion" and
  .rollback_verified == true
' "$release_dir/last.json" >/dev/null

reset_vercel
write_gate
export QIU_MARKET_FIXTURE_PROMOTE_EXIT_AFTER_ALIAS=1
if "$manager" promote --execute "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Uncertain promote result unexpectedly succeeded." >&2
  exit 1
fi
unset QIU_MARKET_FIXTURE_PROMOTE_EXIT_AFTER_ALIAS
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]
jq -e '
  .status == "rolled_back_after_failed_promotion" and
  .rollback_verified == true
' "$release_dir/last.json" >/dev/null

reset_vercel
write_gate
"$manager" promote --execute "${identity_args[@]}" >/dev/null
"$manager" rollback > "$fixture_dir/rollback.json"
[ "$(jq -r '.production_id' "$vercel_state")" = "$previous_id" ]
jq -e '
  .status == "manually-rolled-back" and
  .rollback_verified == true
' "$fixture_dir/rollback.json" >/dev/null

echo "Qiu Market Vercel promotion fixtures passed."
