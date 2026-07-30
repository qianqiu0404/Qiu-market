#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
frontend_root="$repo_root/frontend"
support_dir="$HOME/Library/Application Support/Qiu Market"
production_env="${QIU_MARKET_ENV_FILE:-$support_dir/production.env}"
evidence_file="${QIU_MARKET_PREVIEW_OAUTH_EVIDENCE_FILE:-$support_dir/observations/preview-oauth-evidence.json}"
report_file="${QIU_MARKET_PREVIEW_GATE_REPORT:-$support_dir/observations/preview-gate-latest.json}"
window_report="${QIU_MARKET_PREVIEW_OAUTH_WINDOW_REPORT:-$support_dir/preview-oauth-window/last.json}"
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

for required_command in curl git jq python3 vercel; do
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

validate_close_evidence() {
  python3 - "$1" "$2" "$3" <<'PY'
import datetime
import json
import re
import sys

path, deployment_id, deployment_commit = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    value = json.load(handle)
try:
    opened = datetime.datetime.fromisoformat(
        value["window_opened_at"].replace("Z", "+00:00")
    )
    closed = datetime.datetime.fromisoformat(
        value["completed_at"].replace("Z", "+00:00")
    )
except (KeyError, TypeError, ValueError):
    raise SystemExit(1)
valid = (
    value.get("schema_version") == 1
    and value.get("status") == "closed_after_verified_logout"
    and value.get("deployment_id") == deployment_id
    and value.get("deployment_commit") == deployment_commit
    and re.fullmatch(r"[0-9a-f]{32}", value.get("window_id", "")) is not None
    and value.get("production_configuration_restored") is True
    and value.get("production_oauth_runtime_verified") is True
    and closed >= opened
)
raise SystemExit(0 if valid else 1)
PY
}

validate_browser_evidence() {
  python3 - "$1" "$2" "$3" "$4" "$5" "$6" <<'PY'
import datetime
import json
import sys

(
    path,
    deployment_id,
    deployment_commit,
    window_id,
    window_opened_at,
    maintenance_closed_at,
) = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    value = json.load(handle)
try:
    completed = datetime.datetime.fromisoformat(
        value["completed_at"].replace("Z", "+00:00")
    )
    closed = datetime.datetime.fromisoformat(
        maintenance_closed_at.replace("Z", "+00:00")
    )
except (KeyError, TypeError, ValueError):
    raise SystemExit(1)
required_true = (
    "callback_single_use",
    "secure_cookie",
    "csrf_rejected",
    "origin_rejected",
    "submit_unknown_reconciled",
    "cancel_unknown_reconciled",
    "fund_unknown_reconciled",
    "preview_logout_204",
    "stale_preview_session_401",
    "stale_preview_write_401",
    "visual_trade_page",
)
valid = (
    value.get("schema_version") == 2
    and value.get("deployment_id") == deployment_id
    and value.get("deployment_commit") == deployment_commit
    and value.get("window_id") == window_id
    and value.get("window_opened_at") == window_opened_at
    and value.get("maintenance_closed_at") == maintenance_closed_at
    and all(value.get(name) is True for name in required_true)
    and value.get("console_error_count") == 0
    and completed >= closed
)
raise SystemExit(0 if valid else 1)
PY
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

ordinary_read_request() {
  local label="$1"
  local request_url="$2"
  local code="000"
  local attempt
  for attempt in 1 2 3; do
    code="$(ordinary_request "$label" "$request_url")"
    case "$code" in
      000|5??)
        ;;
      *)
        break
        ;;
    esac
    if [ "$attempt" -lt 3 ]; then
      sleep 1
    fi
  done
  printf '%s\n' "$code"
}

unsigned_funnel_request() {
  local code="000"
  local attempt
  for attempt in 1 2 3; do
    code="$(
      ordinary_request unsigned-funnel \
        "$funnel_origin/api/v2/get_market_overview" \
        --request POST \
        --header 'content-type: application/json' \
        --data '{"consumer_token":"preview-gate","venue":"all","universe":"provider_union"}'
    )"
    case "$code" in
      000|5??)
        ;;
      *)
        break
        ;;
    esac
    if [ "$attempt" -lt 3 ]; then
      sleep 1
    fi
  done
  printf '%s\n' "$code"
}

protected_request() {
  local label="$1"
  local endpoint="$2"
  shift 2
  local result
  local code="000"
  local attempt=1
  while [ "$attempt" -le 3 ]; do
    result="$(
      cd "$frontend_root"
      vercel curl "$endpoint" --deployment "${deployment_url#https://}" -- \
        --silent --show-error --max-time 20 \
        --output "$temp_dir/$label.body" \
        --dump-header "$temp_dir/$label.headers" \
        --write-out '%{http_code}' \
        "$@" 2>"$temp_dir/$label.error" || true
    )"
    code="$(http_code "$result")"
    case "$code" in
      000|5??)
        ;;
      *)
        break
        ;;
    esac
    attempt=$((attempt + 1))
    if [ "$attempt" -le 3 ]; then
      sleep 1
    fi
  done
  printf '%s\n' "$code"
}

inspect_ok=false
inspect_attempt=1
while [ "$inspect_attempt" -le 3 ]; do
  (
    cd "$frontend_root"
    vercel inspect "${deployment_url#https://}" --format=json \
      > "$temp_dir/inspect.json" 2>"$temp_dir/inspect.error"
  ) || true
  if jq -e \
    --arg id "$deployment_id" \
    --arg url "${deployment_url#https://}" '
      .id == $id and
      .url == $url and
      .readyState == "READY" and
      .target == "preview"
    ' "$temp_dir/inspect.json" >/dev/null 2>&1; then
    inspect_ok=true
    break
  fi
  inspect_attempt=$((inspect_attempt + 1))
  if [ "$inspect_attempt" -le 3 ]; then
    sleep 1
  fi
done

frontend_source_match=false
if git -C "$repo_root" cat-file -e "$deployment_commit^{commit}" 2>/dev/null &&
  git -C "$repo_root" diff --quiet "$deployment_commit" HEAD -- frontend; then
  frontend_source_match=true
fi

ordinary_page_http="$(
  ordinary_read_request ordinary-page "$deployment_url/trade/BTC-USDT"
)"
ordinary_api_http="$(
  ordinary_read_request ordinary-api \
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
unsigned_funnel_http="$(unsigned_funnel_request)"

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
observed_provenance="$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Provenance)"
observed_deployment_id="$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Deployment-ID)"
observed_deployment_url="$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Deployment-URL)"
observed_deployment_commit="$(header_value "$temp_dir/trading.headers" X-Qiu-Market-Release-Commit)"
if python3 - \
  "$trading_http" \
  "$observed_provenance" \
  "$observed_deployment_id" \
  "$observed_deployment_url" \
  "$observed_deployment_commit" \
  "$deployment_id" \
  "$deployment_url" \
  "$deployment_commit" <<'PY'
import sys

(
    status,
    provenance,
    observed_id,
    observed_url,
    observed_commit,
    expected_id,
    expected_url,
    expected_commit,
) = sys.argv[1:]
valid = (
    status == "200"
    and provenance == "VERIFIED"
    and observed_id == expected_id
    and observed_url == expected_url
    and observed_commit == expected_commit
)
raise SystemExit(0 if valid else 1)
PY
then
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

managed_oauth_close_evidence=false
closed_window_id=""
closed_window_opened_at=""
closed_at=""
window_report_mode=""
if [ -f "$window_report" ] && [ ! -L "$window_report" ]; then
  window_report_mode="$(stat -f '%Lp' "$window_report")"
fi
if [ "$window_report_mode" = 600 ] || [ "$window_report_mode" = 400 ]; then
  if validate_close_evidence \
    "$window_report" \
    "$deployment_id" \
    "$deployment_commit"; then
    closed_window_id="$(jq -r '.window_id' "$window_report")"
    closed_window_opened_at="$(jq -r '.window_opened_at' "$window_report")"
    closed_at="$(jq -r '.completed_at' "$window_report")"
    managed_oauth_close_evidence=true
  fi
fi

oauth_browser_evidence=false
evidence_mode=""
if [ -f "$evidence_file" ] && [ ! -L "$evidence_file" ]; then
  evidence_mode="$(stat -f '%Lp' "$evidence_file")"
fi
if [ "$managed_oauth_close_evidence" = true ] &&
  { [ "$evidence_mode" = 600 ] || [ "$evidence_mode" = 400 ]; } &&
  [ -s "$evidence_file" ]; then
  if validate_browser_evidence \
    "$evidence_file" \
    "$deployment_id" \
    "$deployment_commit" \
    "$closed_window_id" \
    "$closed_window_opened_at" \
    "$closed_at"; then
    oauth_browser_evidence=true
  fi
fi

if [ "${QIU_MARKET_GATE_DIAGNOSTICS:-false}" = true ]; then
  printf 'preview_gate_diagnostics inspect=%s provenance=%s close_mode=%s close=%s browser_mode=%s browser=%s\n' \
    "$inspect_ok" \
    "$provenance_ok" \
    "$window_report_mode" \
    "$managed_oauth_close_evidence" \
    "$evidence_mode" \
    "$oauth_browser_evidence" >&2
  printf 'preview_gate_identity observed_id=%s observed_url=%s observed_commit=%s\n' \
    "$observed_deployment_id" \
    "$observed_deployment_url" \
    "$observed_deployment_commit" >&2
  if [ -s "$temp_dir/inspect.json" ]; then
    jq -c '{id,url,readyState,target}' "$temp_dir/inspect.json" >&2 || true
  elif [ -s "$temp_dir/inspect.error" ]; then
    sed -n '1p' "$temp_dir/inspect.error" >&2
  fi
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
    reason=managed_oauth_close_evidence_missing
  fi
  if [ "$oauth_private_configured" = true ] &&
    [ "$github_oauth_capability" = true ] &&
    [ "$managed_oauth_close_evidence" = true ]; then
    reason=oauth_browser_evidence_missing
  fi
  if [ "$oauth_private_configured" = true ] &&
    [ "$github_oauth_capability" = true ] &&
    [ "$managed_oauth_close_evidence" = true ] &&
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
  --arg managed_oauth_close_evidence "$managed_oauth_close_evidence" \
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
      managed_oauth_close_evidence_verified:
        ($managed_oauth_close_evidence == "true"),
      oauth_browser_evidence_verified:
        ($oauth_browser_evidence == "true")
    }
  }' > "$temp_dir/report.json"

chmod 600 "$temp_dir/report.json"
mv "$temp_dir/report.json" "$report_file"
chmod 600 "$report_file"
jq . "$report_file"
exit "$exit_code"
