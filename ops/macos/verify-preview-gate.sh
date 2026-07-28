#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
frontend_root="$repo_root/frontend"
support_dir="$HOME/Library/Application Support/Qiu Market"
production_env="${QIU_MARKET_ENV_FILE:-$support_dir/production.env}"
evidence_file="${QIU_MARKET_PREVIEW_OAUTH_EVIDENCE_FILE:-$support_dir/observations/preview-oauth-evidence.json}"
report_file="${QIU_MARKET_PREVIEW_GATE_REPORT:-$support_dir/observations/preview-gate-latest.json}"
funnel_origin="${QIU_MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"

deployment_id=""
deployment_url=""
deployment_commit=""

usage() {
  cat >&2 <<'USAGE'
Usage: verify-preview-gate.sh \
  --deployment-id dpl_... \
  --deployment-url https://immutable-preview.vercel.app \
  --commit <40-hex-sha>

The command is read-only. It verifies the immutable protected Preview and
reports environment-pending until private OAuth configuration and exact
browser-generated OAuth evidence both exist.
USAGE
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --deployment-id)
      [ "$#" -ge 2 ] || usage
      deployment_id="$2"
      shift 2
      ;;
    --deployment-url)
      [ "$#" -ge 2 ] || usage
      deployment_url="${2%/}"
      shift 2
      ;;
    --commit)
      [ "$#" -ge 2 ] || usage
      deployment_commit="$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')"
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

if [[ ! "$deployment_id" =~ ^dpl_[A-Za-z0-9]+$ ]] ||
  [[ ! "$deployment_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]] ||
  [[ ! "$deployment_commit" =~ ^[0-9a-f]{40}$ ]]; then
  usage
fi

for required_command in curl git jq vercel; do
  if ! command -v "$required_command" >/dev/null 2>&1; then
    echo "Required Preview gate dependency is unavailable: $required_command" >&2
    exit 1
  fi
done
if [ ! -f "$frontend_root/.vercel/project.json" ]; then
  echo "The Qiu Market frontend is not linked to Vercel." >&2
  exit 1
fi

report_dir="$(dirname "$report_file")"
mkdir -p "$report_dir"
temp_dir="$(mktemp -d "$report_dir/.preview-gate.XXXXXX")"
cleanup() {
  find "$temp_dir" -depth -delete
}
trap cleanup EXIT

header_value() {
  local header_file="$1"
  local header_name="$2"
  awk -v name="$header_name" '
    BEGIN { IGNORECASE = 1 }
    {
      sub(/\r$/, "")
      split($0, parts, ":")
      if (tolower(parts[1]) == tolower(name)) {
        sub(/^[^:]*:[[:space:]]*/, "", $0)
        value = $0
      }
    }
    END { print value }
  ' "$header_file"
}

http_code() {
  local result="$1"
  printf '%s\n' "$result" |
    awk 'match($0, /[0-9][0-9][0-9]$/) { value = substr($0, RSTART, 3) }
      END { if (value == "") print "000"; else print value }'
}

ordinary_request() {
  local label="$1"
  local request_url="$2"
  shift 2
  local result
  result="$(
    curl --silent --show-error --max-time 20 \
      --output "$temp_dir/$label.body" \
      --dump-header "$temp_dir/$label.headers" \
      --write-out '%{http_code}' \
      "$@" "$request_url" 2>"$temp_dir/$label.error" || true
  )"
  http_code "$result"
}

protected_request() {
  local label="$1"
  local endpoint="$2"
  shift 2
  local result
  result="$(
    cd "$frontend_root"
    vercel curl "$endpoint" --deployment "$deployment_id" -- \
      --silent --show-error \
      --output "$temp_dir/$label.body" \
      --dump-header "$temp_dir/$label.headers" \
      --write-out '%{http_code}' \
      "$@" 2>"$temp_dir/$label.error" || true
  )"
  http_code "$result"
}

inspect_ok=false
if (
  cd "$frontend_root"
  vercel inspect "$deployment_id" --format=json \
    > "$temp_dir/inspect.json" 2>"$temp_dir/inspect.error"
) && jq -e \
  --arg id "$deployment_id" \
  --arg url "${deployment_url#https://}" '
    .id == $id and
    .url == $url and
    .readyState == "READY" and
    .target == "preview"
  ' "$temp_dir/inspect.json" >/dev/null 2>&1; then
  inspect_ok=true
fi

frontend_source_match=false
if git -C "$repo_root" cat-file -e "$deployment_commit^{commit}" 2>/dev/null &&
  git -C "$repo_root" diff --quiet "$deployment_commit" HEAD -- frontend; then
  frontend_source_match=true
fi

ordinary_page_http="$(
  ordinary_request ordinary-page "$deployment_url/trade/BTC-USDT"
)"
ordinary_api_http="$(
  ordinary_request ordinary-api \
    "$deployment_url/api/v1/trading/markets/BTC-USDT/status"
)"

markets_http="$(protected_request markets /markets)"
trade_http="$(protected_request trade /trade/BTC-USDT)"
system_http="$(protected_request system /system)"
trading_http="$(
  protected_request trading \
    /api/v1/trading/markets/BTC-USDT/status
)"
capabilities_http="$(
  protected_request capabilities \
    /api/v1/trading/auth/capabilities
)"
session_http="$(
  protected_request session /api/v1/trading/session
)"
unsigned_funnel_http="$(
  ordinary_request unsigned-funnel \
    "$funnel_origin/api/v2/get_market_overview" \
    --request POST \
    --header 'content-type: application/json' \
    --data '{"consumer_token":"preview-gate","venue":"all","universe":"provider_union"}'
)"

protected_html_ok=false
if [ "$markets_http" = 200 ] &&
  [ "$trade_http" = 200 ] &&
  [ "$system_http" = 200 ] &&
  [[ "$(header_value "$temp_dir/markets.headers" content-type)" == text/html* ]] &&
  [[ "$(header_value "$temp_dir/trade.headers" content-type)" == text/html* ]] &&
  [[ "$(header_value "$temp_dir/system.headers" content-type)" == text/html* ]]; then
  protected_html_ok=true
fi

ordinary_protection_ok=false
if [[ "$ordinary_page_http" =~ ^(302|401|403)$ ]] &&
  [[ "$ordinary_api_http" =~ ^(302|401|403)$ ]]; then
  ordinary_protection_ok=true
fi

provenance_ok=false
if [ "$trading_http" = 200 ] &&
  [ "$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Provenance)" = VERIFIED ] &&
  [ "$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Deployment-ID)" = "$deployment_id" ] &&
  [ "$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Deployment-URL)" = "$deployment_url" ] &&
  [ "$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Release-Commit)" = "$deployment_commit" ]; then
  provenance_ok=true
fi

trading_ready=false
if [ "$trading_http" = 200 ] &&
  jq -e '
    .state == "ready" and
    (.last_error // "") == "" and
    ((.outbox_state // "ready") == "ready") and
    ((.outbox_last_error // "") == "")
  ' "$temp_dir/trading.body" >/dev/null 2>&1; then
  trading_ready=true
fi

github_oauth_capability=false
local_login_disabled=false
if [ "$capabilities_http" = 200 ]; then
  github_oauth_capability="$(
    jq -r '.github_oauth_enabled == true' \
      "$temp_dir/capabilities.body" 2>/dev/null || echo false
  )"
  local_login_disabled="$(
    jq -r '.local_login_enabled == false' \
      "$temp_dir/capabilities.body" 2>/dev/null || echo false
  )"
fi

oauth_private_configured=false
if [ -f "$production_env" ]; then
  # shellcheck disable=SC1090
  source "$production_env"
  oauth_client_id="${MARKET_TRADING_GITHUB_CLIENT_ID:-}"
  oauth_client_secret="${MARKET_TRADING_GITHUB_CLIENT_SECRET:-}"
  if [ -n "$oauth_client_id" ] &&
    [ -n "$oauth_client_secret" ] &&
    [[ "$oauth_client_id" != replace-* ]] &&
    [[ "$oauth_client_secret" != replace-* ]] &&
    [[ "$oauth_client_id" != *CHANGE_ME* ]] &&
    [[ "$oauth_client_secret" != *CHANGE_ME* ]]; then
    oauth_private_configured=true
  fi
  unset oauth_client_id oauth_client_secret
fi

oauth_browser_evidence=false
if [ -s "$evidence_file" ] && jq -e \
  --arg deployment_id "$deployment_id" \
  --arg deployment_commit "$deployment_commit" '
    .schema_version == 1 and
    .deployment_id == $deployment_id and
    .deployment_commit == $deployment_commit and
    .callback_single_use == true and
    .secure_cookie == true and
    .csrf_rejected == true and
    .origin_rejected == true and
    .submit_unknown_reconciled == true and
    .cancel_unknown_reconciled == true and
    .fund_unknown_reconciled == true and
    .preview_logout_204 == true and
    .stale_preview_session_401 == true and
    (.completed_at | type == "string")
  ' "$evidence_file" >/dev/null 2>&1; then
  oauth_browser_evidence=true
fi

non_oauth_checks=false
if [ "$inspect_ok" = true ] &&
  [ "$frontend_source_match" = true ] &&
  [ "$ordinary_protection_ok" = true ] &&
  [ "$protected_html_ok" = true ] &&
  [ "$provenance_ok" = true ] &&
  [ "$trading_ready" = true ] &&
  [ "$session_http" = 401 ] &&
  [ "$unsigned_funnel_http" = 401 ] &&
  [ "$local_login_disabled" = true ]; then
  non_oauth_checks=true
fi

status=failed
reason=non_oauth_preview_check_failed
exit_code=1
if [ "$non_oauth_checks" = true ]; then
  status=environment-pending
  reason=github_oauth_private_configuration_missing
  exit_code=2
  if [ "$oauth_private_configured" = true ] &&
    [ "$github_oauth_capability" = true ]; then
    reason=oauth_browser_evidence_missing
  fi
  if [ "$oauth_private_configured" = true ] &&
    [ "$github_oauth_capability" = true ] &&
    [ "$oauth_browser_evidence" = true ]; then
    status=preview-gate-passed
    reason=all_preview_security_evidence_verified
    exit_code=0
  fi
fi

checked_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
jq -n \
  --arg checked_at "$checked_at" \
  --arg status "$status" \
  --arg reason "$reason" \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$deployment_commit" \
  --arg inspect_ok "$inspect_ok" \
  --arg frontend_source_match "$frontend_source_match" \
  --arg ordinary_protection_ok "$ordinary_protection_ok" \
  --arg protected_html_ok "$protected_html_ok" \
  --arg provenance_ok "$provenance_ok" \
  --arg trading_ready "$trading_ready" \
  --arg session_http "$session_http" \
  --arg unsigned_funnel_http "$unsigned_funnel_http" \
  --arg local_login_disabled "$local_login_disabled" \
  --arg oauth_private_configured "$oauth_private_configured" \
  --arg github_oauth_capability "$github_oauth_capability" \
  --arg oauth_browser_evidence "$oauth_browser_evidence" \
  '{
    schema_version: 1,
    checked_at: $checked_at,
    status: $status,
    reason: $reason,
    deployment: {
      id: $deployment_id,
      url: $deployment_url,
      commit: $deployment_commit
    },
    checks: {
      inspect_ready_preview: ($inspect_ok == "true"),
      local_frontend_matches_release: ($frontend_source_match == "true"),
      vercel_authentication_blocks_ordinary_access:
        ($ordinary_protection_ok == "true"),
      protected_spa_deep_links_200: ($protected_html_ok == "true"),
      runtime_provenance_matches: ($provenance_ok == "true"),
      trading_and_outbox_ready: ($trading_ready == "true"),
      anonymous_session_http: ($session_http | tonumber? // 0),
      unsigned_funnel_rest_http: ($unsigned_funnel_http | tonumber? // 0),
      local_login_disabled: ($local_login_disabled == "true"),
      oauth_private_configuration_present:
        ($oauth_private_configured == "true"),
      github_oauth_runtime_capability:
        ($github_oauth_capability == "true"),
      oauth_browser_evidence_verified:
        ($oauth_browser_evidence == "true")
    }
  }' > "$temp_dir/report.json"

chmod 600 "$temp_dir/report.json"
mv "$temp_dir/report.json" "$report_file"
chmod 600 "$report_file"
jq . "$report_file"
exit "$exit_code"
