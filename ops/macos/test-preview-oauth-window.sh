#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_bin="$repo_root/ops/macos/fixtures/preview-oauth-window-bin"
fixture_dir="$(mktemp -d /tmp/qiu-market-preview-oauth-window.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

support_dir="$fixture_dir/support"
production_env="$support_dir/production.env"
control_log="$fixture_dir/control.log"
mkdir -p "$support_dir/observations"
chmod 700 "$support_dir"
touch "$control_log"

cat > "$production_env" <<'ENV'
MARKET_TRADING_LOCAL_AUTH=false
MARKET_TRADING_SECURE_COOKIES=true
MARKET_TRADING_ALLOWED_ORIGINS=https://qiu-market.vercel.app
MARKET_TRADING_GITHUB_CLIENT_ID=fixture-client-id
MARKET_TRADING_GITHUB_CLIENT_SECRET=fixture-client-secret
MARKET_TRADING_GITHUB_REDIRECT_URL=https://qiu-market.vercel.app/api/v1/trading/auth/github/callback
ENV
chmod 600 "$production_env"

export PATH="$fixture_bin:$PATH"
export QIU_MARKET_SUPPORT_DIR="$support_dir"
export QIU_MARKET_ENV_FILE="$production_env"
export QIU_MARKET_PREVIEW_OAUTH_WINDOW_DIR="$support_dir/preview-oauth-window"
export QIU_MARKET_PREVIEW_GATE_VERIFIER="$fixture_bin/preview-verifier"
export QIU_MARKET_API_CONTROLLER="$fixture_bin/api-controller"
export QIU_MARKET_FIXTURE_CONTROL_LOG="$control_log"

manager="$repo_root/ops/macos/manage-preview-oauth-window.sh"
deployment_id="dpl_PreviewOAuthFixture"
deployment_url="https://qiu-market-fixture-preview.vercel.app"
deployment_commit="0123456789abcdef0123456789abcdef01234567"
identity_args=(
  --deployment-id "$deployment_id"
  --deployment-url "$deployment_url"
  --commit "$deployment_commit"
)

before_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
"$manager" preflight "${identity_args[@]}" > "$fixture_dir/preflight.json"
after_preflight_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
[ "$before_sha" = "$after_preflight_sha" ]
jq -e '.status == "preflight-passed" and .mutated == false' \
  "$fixture_dir/preflight.json" >/dev/null

mkdir -p "$support_dir/preview-oauth-window/operation.lock"
if "$manager" open "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Open unexpectedly ignored an active operation lock." >&2
  exit 1
fi
rmdir "$support_dir/preview-oauth-window/operation.lock"
after_locked_open_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
[ "$before_sha" = "$after_locked_open_sha" ]

mkdir "$support_dir/preview-oauth-window/operation.lock"
printf '999999\n' > "$support_dir/preview-oauth-window/operation.lock/pid"
"$manager" open "${identity_args[@]}" > "$fixture_dir/open.json"
jq -e '.phase == "open"' "$fixture_dir/open.json" >/dev/null
grep -Fx \
  "MARKET_TRADING_ALLOWED_ORIGINS=https://qiu-market.vercel.app,$deployment_url" \
  "$production_env" >/dev/null
grep -Fx \
  "MARKET_TRADING_GITHUB_REDIRECT_URL=$deployment_url/api/v1/trading/auth/github/callback" \
  "$production_env" >/dev/null
[ "$(stat -f '%Lp' "$production_env")" = 600 ]

if "$manager" open "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Second open unexpectedly succeeded." >&2
  exit 1
fi
if "$manager" close >/dev/null 2>&1; then
  echo "Close without browser evidence unexpectedly succeeded." >&2
  exit 1
fi

jq -n \
  --arg deployment_id "$deployment_id" \
  --arg deployment_commit "$deployment_commit" \
  --arg window_id "$(jq -r '.window_id' "$support_dir/preview-oauth-window/active.json")" \
  --arg window_opened_at "$(jq -r '.opened_at' "$support_dir/preview-oauth-window/active.json")" '
    {
      schema_version: 1,
      deployment_id: $deployment_id,
      deployment_commit: $deployment_commit,
      window_id: $window_id,
      window_opened_at: $window_opened_at,
      callback_single_use: true,
      secure_cookie: true,
      csrf_rejected: true,
      origin_rejected: true,
      submit_unknown_reconciled: true,
      cancel_unknown_reconciled: true,
      fund_unknown_reconciled: true,
      preview_logout_204: true,
      completed_at: $window_opened_at
    }
  ' > "$support_dir/observations/preview-oauth-preclose-evidence.json"
chmod 600 "$support_dir/observations/preview-oauth-preclose-evidence.json"

"$manager" close > "$fixture_dir/close.json"
after_close_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
[ "$before_sha" = "$after_close_sha" ]
jq -e '
  .status == "closed_after_verified_logout" and
  .production_configuration_restored == true and
  .production_oauth_runtime_verified == true
' "$fixture_dir/close.json" >/dev/null
[ ! -e "$support_dir/preview-oauth-window/active.json" ]
[ ! -e "$support_dir/preview-oauth-window/production.env.before-preview" ]

export QIU_MARKET_FIXTURE_PREVIEW_FAIL=1
if "$manager" open "${identity_args[@]}" >/dev/null 2>&1; then
  echo "Open with failed Preview runtime unexpectedly succeeded." >&2
  exit 1
fi
unset QIU_MARKET_FIXTURE_PREVIEW_FAIL
after_failed_open_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
[ "$before_sha" = "$after_failed_open_sha" ]
[ ! -e "$support_dir/preview-oauth-window/active.json" ]

"$manager" open "${identity_args[@]}" >/dev/null
"$manager" abort > "$fixture_dir/abort.json"
after_abort_sha="$(shasum -a 256 "$production_env" | awk '{print $1}')"
[ "$before_sha" = "$after_abort_sha" ]
jq -e '.status == "aborted_without_acceptance"' "$fixture_dir/abort.json" >/dev/null

[ "$(grep -c '^restart$' "$control_log")" -eq 6 ]
[ "$(grep -c '^wait$' "$control_log")" -eq 6 ]

echo "Preview OAuth maintenance-window fixtures passed."
