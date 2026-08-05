#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
if [ "$#" -gt 0 ]; then
  shift
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=ops/macos/production-lib.sh
source "$repo_root/ops/macos/production-lib.sh"

support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
production_env="${QIU_MARKET_ENV_FILE:-$support_dir/production.env}"
window_dir="${QIU_MARKET_PREVIEW_OAUTH_WINDOW_DIR:-$support_dir/preview-oauth-window}"
state_file="$window_dir/active.json"
backup_file="$window_dir/production.env.before-preview"
last_report="$window_dir/last.json"
preflight_report="$window_dir/preflight.json"
preclose_evidence="${QIU_MARKET_PREVIEW_OAUTH_PRECLOSE_EVIDENCE_FILE:-$support_dir/observations/preview-oauth-preclose-evidence.json}"
preview_verifier="${QIU_MARKET_PREVIEW_GATE_VERIFIER:-$repo_root/ops/macos/verify-preview-gate.sh}"
api_controller="${QIU_MARKET_API_CONTROLLER:-}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"
production_callback="$production_origin/api/v1/trading/auth/github/callback"
operation_lock="$window_dir/operation.lock"

deployment_id=""
deployment_url=""
deployment_commit=""
window_id=""
window_opened_at=""
rollback_required=false
lock_owned=false

usage() {
  cat >&2 <<'USAGE'
Usage:
  manage-preview-oauth-window.sh status
  manage-preview-oauth-window.sh preflight --deployment-id dpl_... \
    --deployment-url https://immutable-preview.vercel.app --commit <40-hex-sha>
  manage-preview-oauth-window.sh open --deployment-id dpl_... \
    --deployment-url https://immutable-preview.vercel.app --commit <40-hex-sha>
  manage-preview-oauth-window.sh close
  manage-preview-oauth-window.sh abort

preflight is read-only. open temporarily adds the exact Preview origin and
switches the fixed OAuth callback, then restarts only the API. close requires
browser-generated pre-close evidence proving logout returned 204. abort is the
emergency restoration path and never treats the Preview gate as accepted.
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

require_exact_preview_identity() {
  if [[ ! "$deployment_id" =~ ^dpl_[A-Za-z0-9]+$ ]] ||
    [[ ! "$deployment_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]] ||
    [[ ! "$deployment_commit" =~ ^[0-9a-f]{40}$ ]]; then
    usage
  fi
}

prepare_private_directory() {
  mkdir -p "$window_dir"
  chmod 700 "$window_dir"
}

acquire_operation_lock() {
  prepare_private_directory
  if ! mkdir "$operation_lock" 2>/dev/null; then
    lock_pid=""
    if [ -f "$operation_lock/pid" ]; then
      lock_pid="$(awk 'NR == 1 { print; exit }' "$operation_lock/pid")"
    fi
    if [[ "$lock_pid" =~ ^[0-9]+$ ]] && ! kill -0 "$lock_pid" 2>/dev/null; then
      rm -f "$operation_lock/pid"
      rmdir "$operation_lock" 2>/dev/null || true
    fi
    if ! mkdir "$operation_lock" 2>/dev/null; then
      echo "Another Preview OAuth maintenance operation is active: $operation_lock" >&2
      return 1
    fi
  fi
  chmod 700 "$operation_lock"
  printf '%s\n' "$$" > "$operation_lock/pid"
  chmod 600 "$operation_lock/pid"
  lock_owned=true
}

release_operation_lock() {
  if [ "$lock_owned" = true ]; then
    rm -f "$operation_lock/pid"
    rmdir "$operation_lock" 2>/dev/null || true
    lock_owned=false
  fi
}

require_environment_file() {
  QIU_MARKET_ENV_FILE="$production_env"
  export QIU_MARKET_ENV_FILE
  qiu_require_private_environment
  if [ -L "$production_env" ]; then
    echo "Refusing a symlinked private production environment: $production_env" >&2
    return 1
  fi
}

env_key_count() {
  local key="$1"
  awk -v key="$key" '
    $0 ~ "^[[:space:]]*" key "=" { count += 1 }
    END { print count + 0 }
  ' "$production_env"
}

require_single_env_key() {
  local key="$1"
  local count
  count="$(env_key_count "$key")"
  if [ "$count" != 1 ]; then
    echo "Private environment must define $key exactly once; found $count." >&2
    return 1
  fi
}

read_env_value() {
  local key="$1"
  (
    unset "$key"
    # shellcheck disable=SC1090
    source "$production_env"
    printf '%s' "${!key:-}"
  )
}

is_placeholder() {
  local value="$1"
  [ -z "$value" ] ||
    [[ "$value" == replace-* ]] ||
    [[ "$value" == *CHANGE_ME* ]]
}

origin_list_contains() {
  local list="$1"
  local expected="$2"
  local item
  local old_ifs="$IFS"
  IFS=,
  for item in $list; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    if [ "$item" = "$expected" ]; then
      IFS="$old_ifs"
      return 0
    fi
  done
  IFS="$old_ifs"
  return 1
}

rewrite_environment() {
  local allowed_origins="$1"
  local redirect_url="$2"
  local temp_file
  temp_file="$(mktemp "$window_dir/.production.env.XXXXXX")"
  chmod 600 "$temp_file"
  awk \
    -v allowed="$allowed_origins" \
    -v redirect="$redirect_url" '
      /^[[:space:]]*MARKET_TRADING_ALLOWED_ORIGINS=/ {
        print "MARKET_TRADING_ALLOWED_ORIGINS=" allowed
        next
      }
      /^[[:space:]]*MARKET_TRADING_GITHUB_REDIRECT_URL=/ {
        print "MARKET_TRADING_GITHUB_REDIRECT_URL=" redirect
        next
      }
      { print }
    ' "$production_env" > "$temp_file"
  mv -f "$temp_file" "$production_env"
  chmod 600 "$production_env"
}

restart_and_wait_for_api() {
  if [ -n "$api_controller" ]; then
    if [ ! -x "$api_controller" ]; then
      echo "Configured API controller is not executable: $api_controller" >&2
      return 1
    fi
    "$api_controller" restart
    "$api_controller" wait
    return
  fi
  qiu_load_production_environment "$repo_root"
  qiu_restart_role api
  qiu_wait_for_api 60
  qiu_wait_for_trading_status true 120 >/dev/null
}

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

header_value_containing() {
  local header_file="$1"
  local header_name="$2"
  local required_text="$3"
  awk -v name="$header_name" -v required="$required_text" '
    BEGIN { IGNORECASE = 1 }
    {
      sub(/\r$/, "")
      split($0, parts, ":")
      if (tolower(parts[1]) == tolower(name) &&
          index(tolower($0), tolower(required)) > 0) {
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

preview_request() {
  local label="$1"
  local endpoint="$2"
  local result
  result="$(
    cd "$repo_root/frontend"
    vercel curl "$endpoint" --deployment "$deployment_id" -- \
      --silent --show-error --max-time 20 --max-redirs 0 \
      --output "$window_dir/$label.body" \
      --dump-header "$window_dir/$label.headers" \
      --write-out '%{http_code}' 2>"$window_dir/$label.error" || true
  )"
  http_code "$result"
}

production_request() {
  local label="$1"
  local endpoint="$2"
  local result
  result="$(
    curl --silent --show-error --max-time 20 --max-redirs 0 \
      --output "$window_dir/$label.body" \
      --dump-header "$window_dir/$label.headers" \
      --write-out '%{http_code}' \
      "$production_origin$endpoint" 2>"$window_dir/$label.error" || true
  )"
  http_code "$result"
}

authorization_redirect_matches() {
  local location="$1"
  local expected_callback="$2"
  python3 - "$location" "$expected_callback" <<'PY'
import sys
from urllib.parse import parse_qs, urlparse

location, expected = sys.argv[1:3]
parsed = urlparse(location)
query = parse_qs(parsed.query)
valid_host = parsed.scheme == "https" and parsed.netloc == "github.com"
valid_path = parsed.path == "/login/oauth/authorize"
raise SystemExit(0 if valid_host and valid_path and query.get("redirect_uri") == [expected] else 1)
PY
}

verify_oauth_runtime() {
  local mode="$1"
  local callback="$2"
  local capabilities_http="000"
  local capabilities_attempt=1
  local start_http
  while [ "$capabilities_attempt" -le 3 ]; do
    if [ "$mode" = preview ]; then
      capabilities_http="$(preview_request runtime-capabilities /api/v1/trading/auth/capabilities)"
    else
      capabilities_http="$(production_request runtime-capabilities /api/v1/trading/auth/capabilities)"
    fi
    case "$capabilities_http" in
      000|5??)
        ;;
      *)
        break
        ;;
    esac
    capabilities_attempt=$((capabilities_attempt + 1))
    if [ "$capabilities_attempt" -le 3 ]; then
      sleep 1
    fi
  done

  if [ "$capabilities_http" != 200 ] ||
    ! jq -e '
      .github_oauth_enabled == true and
      .local_login_enabled == false
    ' "$window_dir/runtime-capabilities.body" >/dev/null 2>&1; then
    echo "OAuth runtime capabilities are not safe after the API restart." >&2
    return 1
  fi
  if [ "$mode" = preview ]; then
    start_http="$(preview_request runtime-oauth-start /api/v1/trading/auth/github/start)"
  else
    start_http="$(production_request runtime-oauth-start /api/v1/trading/auth/github/start)"
  fi
  if [ "$start_http" != 302 ]; then
    echo "OAuth start did not return the required single redirect; HTTP $start_http." >&2
    return 1
  fi

  local location
  local state_cookie
  local state_cookie_lower
  location="$(header_value "$window_dir/runtime-oauth-start.headers" location)"
  state_cookie="$(
    header_value_containing \
      "$window_dir/runtime-oauth-start.headers" \
      set-cookie \
      s78_trading_oauth_state=
  )"
  state_cookie_lower="$(printf '%s' "$state_cookie" | tr '[:upper:]' '[:lower:]')"
  if ! authorization_redirect_matches "$location" "$callback"; then
    echo "OAuth start did not bind redirect_uri to the expected fixed callback." >&2
    return 1
  fi
  if [[ "$state_cookie" != *s78_trading_oauth_state=* ]] ||
    [[ "$state_cookie_lower" != *secure* ]] ||
    [[ "$state_cookie_lower" != *httponly* ]] ||
    [[ "$state_cookie_lower" != *samesite=lax* ]]; then
    echo "OAuth state cookie is missing Secure, HttpOnly, or SameSite=Lax." >&2
    return 1
  fi
}

write_state() {
  local phase="$1"
  local original_allowed="$2"
  local original_redirect="$3"
  local backup_sha="$4"
  local opened_at="${5:-}"
  local temp_file
  temp_file="$(mktemp "$window_dir/.state.XXXXXX")"
  jq -n \
    --arg phase "$phase" \
    --arg deployment_id "$deployment_id" \
    --arg deployment_url "$deployment_url" \
    --arg deployment_commit "$deployment_commit" \
    --arg window_id "$window_id" \
    --arg production_origin "$production_origin" \
    --arg production_callback "$production_callback" \
    --arg preview_callback "$deployment_url/api/v1/trading/auth/github/callback" \
    --arg original_allowed_origins "$original_allowed" \
    --arg original_redirect_url "$original_redirect" \
    --arg backup_sha256 "$backup_sha" \
    --arg opened_at "$opened_at" \
    --arg updated_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '
      {
        schema_version: 1,
        phase: $phase,
        deployment_id: $deployment_id,
        deployment_url: $deployment_url,
        deployment_commit: $deployment_commit,
        window_id: $window_id,
        production_origin: $production_origin,
        production_callback: $production_callback,
        preview_callback: $preview_callback,
        original_allowed_origins: $original_allowed_origins,
        original_redirect_url: $original_redirect_url,
        backup_sha256: $backup_sha256,
        opened_at: $opened_at,
        updated_at: $updated_at
      }
    ' > "$temp_file"
  chmod 600 "$temp_file"
  mv -f "$temp_file" "$state_file"
}

load_identity_from_state() {
  deployment_id="$(jq -r '.deployment_id // ""' "$state_file")"
  deployment_url="$(jq -r '.deployment_url // ""' "$state_file")"
  deployment_commit="$(jq -r '.deployment_commit // ""' "$state_file")"
  window_id="$(jq -r '.window_id // ""' "$state_file")"
  window_opened_at="$(jq -r '.opened_at // ""' "$state_file")"
  production_origin="$(jq -r '.production_origin // ""' "$state_file")"
  production_callback="$(jq -r '.production_callback // ""' "$state_file")"
}

restore_environment_from_backup() {
  local expected_sha
  local actual_sha
  expected_sha="$(jq -r '.backup_sha256 // ""' "$state_file")"
  if [ ! -f "$backup_file" ]; then
    echo "Preview OAuth environment backup is missing: $backup_file" >&2
    return 1
  fi
  actual_sha="$(qiu_sha256 "$backup_file")"
  if [ -z "$expected_sha" ] || [ "$actual_sha" != "$expected_sha" ]; then
    echo "Preview OAuth environment backup checksum does not match state." >&2
    return 1
  fi
  local temp_file
  temp_file="$(mktemp "$window_dir/.restore.XXXXXX")"
  cp "$backup_file" "$temp_file"
  chmod 600 "$temp_file"
  mv -f "$temp_file" "$production_env"
  chmod 600 "$production_env"
}

finish_restoration() {
  local final_status="$1"
  restore_environment_from_backup
  restart_and_wait_for_api
  verify_oauth_runtime production "$production_callback"

  jq -n \
    --arg status "$final_status" \
    --arg deployment_id "$deployment_id" \
    --arg deployment_url "$deployment_url" \
    --arg deployment_commit "$deployment_commit" \
    --arg window_id "$window_id" \
    --arg window_opened_at "$window_opened_at" \
    --arg completed_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '
      {
        schema_version: 1,
        status: $status,
        deployment_id: $deployment_id,
        deployment_url: $deployment_url,
        deployment_commit: $deployment_commit,
        window_id: $window_id,
        window_opened_at: $window_opened_at,
        production_configuration_restored: true,
        production_oauth_runtime_verified: true,
        completed_at: $completed_at
      }
    ' > "$last_report"
  chmod 600 "$last_report"
  rm -f "$state_file" "$backup_file"
}

rollback_open_failure() {
  local exit_code="$?"
  trap - EXIT HUP INT TERM
  if [ "$rollback_required" = true ]; then
    echo "Preview OAuth open failed; restoring the exact Production environment." >&2
    if finish_restoration aborted_after_open_failure; then
      echo "Production OAuth configuration restored after failed open." >&2
    else
      echo "Automatic restoration could not be fully verified; backup and state were preserved." >&2
    fi
  fi
  release_operation_lock
  exit "$exit_code"
}

read_only_preflight() {
  require_exact_preview_identity
  require_environment_file
  prepare_private_directory
  if [ -f "$state_file" ]; then
    echo "A Preview OAuth maintenance window is already active." >&2
    return 1
  fi
  for key in \
    MARKET_TRADING_ALLOWED_ORIGINS \
    MARKET_TRADING_LOCAL_AUTH \
    MARKET_TRADING_SECURE_COOKIES \
    MARKET_TRADING_GITHUB_CLIENT_ID \
    MARKET_TRADING_GITHUB_CLIENT_SECRET \
    MARKET_TRADING_GITHUB_REDIRECT_URL; do
    require_single_env_key "$key"
  done

  local allowed_origins
  local local_auth
  local secure_cookies
  local client_id
  local client_secret
  local redirect_url
  allowed_origins="$(read_env_value MARKET_TRADING_ALLOWED_ORIGINS)"
  local_auth="$(read_env_value MARKET_TRADING_LOCAL_AUTH)"
  secure_cookies="$(read_env_value MARKET_TRADING_SECURE_COOKIES)"
  client_id="$(read_env_value MARKET_TRADING_GITHUB_CLIENT_ID)"
  client_secret="$(read_env_value MARKET_TRADING_GITHUB_CLIENT_SECRET)"
  redirect_url="$(read_env_value MARKET_TRADING_GITHUB_REDIRECT_URL)"

  if is_placeholder "$client_id" || is_placeholder "$client_secret"; then
    echo "GitHub OAuth credentials are still missing from the private environment." >&2
    return 2
  fi
  if [ "$local_auth" != false ] || [ "$secure_cookies" != true ]; then
    echo "Preview OAuth requires local auth false and secure cookies true." >&2
    return 1
  fi
  if [ "$redirect_url" != "$production_callback" ]; then
    echo "Production OAuth callback is not at the required baseline." >&2
    return 1
  fi
  if ! origin_list_contains "$allowed_origins" "$production_origin"; then
    echo "Production origin is missing from the allowed-origin baseline." >&2
    return 1
  fi
  if origin_list_contains "$allowed_origins" "$deployment_url"; then
    echo "Exact Preview origin is already present without managed window state." >&2
    return 1
  fi

  for required_command in curl jq openssl python3 shasum vercel; do
    qiu_require_command "$required_command"
  done
  if [ ! -x "$preview_verifier" ]; then
    echo "Preview gate verifier is unavailable: $preview_verifier" >&2
    return 1
  fi

  local verifier_status=0
  QIU_MARKET_ENV_FILE="$production_env" \
    QIU_MARKET_PREVIEW_GATE_REPORT="$preflight_report" \
    "$preview_verifier" \
      --deployment-id "$deployment_id" \
      --deployment-url "$deployment_url" \
      --commit "$deployment_commit" || verifier_status=$?
  if [ "$verifier_status" != 0 ] && [ "$verifier_status" != 2 ]; then
    echo "Immutable Preview preflight failed." >&2
    return 1
  fi
  if ! jq -e '
    .checks.inspect_ready_preview == true and
    .checks.local_frontend_matches_release == true and
    .checks.vercel_authentication_blocks_ordinary_access == true and
    .checks.protected_spa_deep_links_200 == true and
    .checks.runtime_provenance_matches == true and
    .checks.trading_and_outbox_ready == true and
    .checks.anonymous_session_http == 401 and
    .checks.unsigned_funnel_rest_http == 401 and
    .checks.local_login_disabled == true and
    .checks.oauth_private_configuration_present == true
  ' "$preflight_report" >/dev/null 2>&1; then
    echo "Immutable Preview preflight report is missing a required safety check." >&2
    return 1
  fi
}

case "$action" in
  status)
    prepare_private_directory
    if [ -f "$state_file" ]; then
      jq '{
        schema_version,
        phase,
        deployment_id,
        deployment_url,
        deployment_commit,
        window_id,
        opened_at,
        updated_at
      }' "$state_file"
    elif [ -f "$last_report" ]; then
      jq '.' "$last_report"
    else
      jq -n '{schema_version: 1, status: "closed", reason: "never_opened"}'
    fi
    ;;
  preflight)
    read_only_preflight
    jq -n \
      --arg deployment_id "$deployment_id" \
      --arg deployment_url "$deployment_url" \
      --arg deployment_commit "$deployment_commit" '
        {
          schema_version: 1,
          status: "preflight-passed",
          deployment_id: $deployment_id,
          deployment_url: $deployment_url,
          deployment_commit: $deployment_commit,
          mutated: false
        }
      '
    ;;
  open)
    acquire_operation_lock
    trap release_operation_lock EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    read_only_preflight
    original_allowed="$(read_env_value MARKET_TRADING_ALLOWED_ORIGINS)"
    original_redirect="$(read_env_value MARKET_TRADING_GITHUB_REDIRECT_URL)"
    preview_callback="$deployment_url/api/v1/trading/auth/github/callback"
    cp -p "$production_env" "$backup_file"
    chmod 600 "$backup_file"
    backup_sha="$(qiu_sha256 "$backup_file")"
    window_id="$(openssl rand -hex 16)"
    write_state opening "$original_allowed" "$original_redirect" "$backup_sha"
    rollback_required=true
    trap rollback_open_failure EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM

    rewrite_environment "$original_allowed,$deployment_url" "$preview_callback"
    restart_and_wait_for_api
    verify_oauth_runtime preview "$preview_callback"
    window_opened_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    write_state \
      open \
      "$original_allowed" \
      "$original_redirect" \
      "$backup_sha" \
      "$window_opened_at"

    rollback_required=false
    trap - EXIT HUP INT TERM
    release_operation_lock
    jq '{
      schema_version,
      phase,
      deployment_id,
      deployment_url,
      deployment_commit,
      window_id,
      preview_callback,
      opened_at
    }' "$state_file"
    ;;
  close)
    acquire_operation_lock
    trap release_operation_lock EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    require_environment_file
    if [ ! -f "$state_file" ]; then
      echo "No Preview OAuth maintenance window is active." >&2
      exit 1
    fi
    load_identity_from_state
    if [ "$(jq -r '.phase // ""' "$state_file")" != open ]; then
      echo "Preview OAuth window is not in the closeable open phase." >&2
      exit 1
    fi
    opened_at="$window_opened_at"
    evidence_mode=""
    if [ -f "$preclose_evidence" ] && [ ! -L "$preclose_evidence" ]; then
      evidence_mode="$(stat -f '%Lp' "$preclose_evidence")"
    fi
    if { [ "$evidence_mode" != 600 ] && [ "$evidence_mode" != 400 ]; } ||
      [ ! -s "$preclose_evidence" ] || ! jq -e \
      --arg deployment_id "$deployment_id" \
      --arg deployment_commit "$deployment_commit" \
      --arg window_id "$window_id" \
      --arg opened_at "$opened_at" '
        .schema_version == 1 and
        .deployment_id == $deployment_id and
        .deployment_commit == $deployment_commit and
        .window_id == $window_id and
        .window_opened_at == $opened_at and
        .callback_single_use == true and
        .secure_cookie == true and
        .csrf_rejected == true and
        .origin_rejected == true and
        .submit_unknown_reconciled == true and
        .cancel_unknown_reconciled == true and
        .fund_unknown_reconciled == true and
        .preview_logout_204 == true and
        ((.completed_at // "") >= $opened_at)
      ' "$preclose_evidence" >/dev/null 2>&1; then
      echo "Browser-generated pre-close OAuth/logout evidence is missing or mismatched." >&2
      exit 2
    fi
    if ! finish_restoration closed_after_verified_logout; then
      echo "Production restoration failed; backup and state remain for abort/retry." >&2
      exit 1
    fi
    jq '.' "$last_report"
    ;;
  abort)
    acquire_operation_lock
    trap release_operation_lock EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    require_environment_file
    if [ ! -f "$state_file" ]; then
      echo "No Preview OAuth maintenance window is active." >&2
      exit 1
    fi
    load_identity_from_state
    if ! finish_restoration aborted_without_acceptance; then
      echo "Production restoration failed; backup and state remain for retry." >&2
      exit 1
    fi
    jq '.' "$last_report"
    ;;
  *)
    usage
    ;;
esac
