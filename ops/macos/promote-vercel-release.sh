#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
if [ "$#" -gt 0 ]; then
  shift
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
frontend_root="$repo_root/frontend"
vercel_project_file="${QIU_MARKET_VERCEL_PROJECT_FILE:-$frontend_root/.vercel/project.json}"
support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
release_dir="${QIU_MARKET_VERCEL_RELEASE_DIR:-$support_dir/vercel-release}"
gate_report="${QIU_MARKET_PREVIEW_GATE_REPORT:-$support_dir/observations/preview-gate-latest.json}"
production_evidence="${QIU_MARKET_PRODUCTION_AUTH_EVIDENCE_FILE:-$support_dir/observations/production-auth-evidence.json}"
oauth_window_state="${QIU_MARKET_PREVIEW_OAUTH_WINDOW_STATE:-$support_dir/preview-oauth-window/active.json}"
acceptance_epoch="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$support_dir/observations/acceptance-epoch.json}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"
funnel_origin="${QIU_MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"
promotion_smoke_attempts="${QIU_MARKET_PROMOTION_SMOKE_ATTEMPTS:-6}"
promotion_smoke_interval="${QIU_MARKET_PROMOTION_SMOKE_INTERVAL_SECONDS:-5}"
promotion_resolve_attempts="${QIU_MARKET_PROMOTION_RESOLVE_ATTEMPTS:-10}"
promotion_resolve_interval="${QIU_MARKET_PROMOTION_RESOLVE_INTERVAL_SECONDS:-2}"
state_file="$release_dir/active.json"
last_report="$release_dir/last.json"
lock_dir="$release_dir/operation.lock"

deployment_id=""
deployment_url=""
deployment_commit=""
promoted_id=""
promoted_url=""
execute_promotion=false
lock_owned=false
promotion_may_be_applied=false
promotion_started_at_ms=0

usage() {
  cat >&2 <<'USAGE'
Usage:
  promote-vercel-release.sh status
  promote-vercel-release.sh preflight --deployment-id dpl_... \
    --deployment-url https://immutable-preview.vercel.app --commit <40-hex-sha>
  promote-vercel-release.sh promote --execute --deployment-id dpl_... \
    --deployment-url https://immutable-preview.vercel.app --commit <40-hex-sha> \
    [--promoted-id dpl_... --promoted-url https://production-copy.vercel.app]
  promote-vercel-release.sh confirm
  promote-vercel-release.sh rollback

preflight is read-only. promote is refused unless the exact protected Preview
gate passed recently. It uses `vercel promote`; it never builds or deploys a
new artifact. Structural post-promotion failure rolls back the previous
Production deployment. confirm requires browser-generated Production auth/write
evidence. rollback re-points Production to the recorded previous deployment.
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
    --promoted-id)
      [ "$#" -ge 2 ] || usage
      promoted_id="$2"
      shift 2
      ;;
    --promoted-url)
      [ "$#" -ge 2 ] || usage
      promoted_url="${2%/}"
      shift 2
      ;;
    --execute)
      execute_promotion=true
      shift
      ;;
    *)
      usage
      ;;
  esac
done

prepare_private_dir() {
  mkdir -p "$release_dir"
  chmod 700 "$release_dir"
}

acquire_lock() {
  prepare_private_dir
  if ! mkdir "$lock_dir" 2>/dev/null; then
    lock_pid=""
    if [ -f "$lock_dir/pid" ]; then
      lock_pid="$(awk 'NR == 1 { print; exit }' "$lock_dir/pid")"
    fi
    if [[ "$lock_pid" =~ ^[0-9]+$ ]] && ! kill -0 "$lock_pid" 2>/dev/null; then
      rm -f "$lock_dir/pid"
      rmdir "$lock_dir" 2>/dev/null || true
    fi
    if ! mkdir "$lock_dir" 2>/dev/null; then
      echo "Another Vercel release operation is active: $lock_dir" >&2
      return 1
    fi
  fi
  chmod 700 "$lock_dir"
  printf '%s\n' "$$" > "$lock_dir/pid"
  chmod 600 "$lock_dir/pid"
  lock_owned=true
}

release_lock() {
  if [ "$lock_owned" = true ]; then
    rm -f "$lock_dir/pid"
    rmdir "$lock_dir" 2>/dev/null || true
    lock_owned=false
  fi
}

require_identity() {
  if [[ ! "$deployment_id" =~ ^dpl_[A-Za-z0-9]+$ ]] ||
    [[ ! "$deployment_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]] ||
    [[ ! "$deployment_commit" =~ ^[0-9a-f]{40}$ ]]; then
    usage
  fi
  if [ -n "$promoted_id$promoted_url" ] &&
    { [[ ! "$promoted_id" =~ ^dpl_[A-Za-z0-9]+$ ]] ||
      [[ ! "$promoted_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]]; }; then
    usage
  fi
}

require_commands() {
  local name
  for name in curl git jq openssl python3 vercel; do
    if ! command -v "$name" >/dev/null 2>&1; then
      echo "Required release dependency is unavailable: $name" >&2
      return 1
    fi
  done
  if [ ! -f "$vercel_project_file" ]; then
    echo "Qiu Market frontend is not linked to Vercel." >&2
    return 1
  fi
}

project_identity() {
  jq -er "$1 // empty" "$vercel_project_file"
}

private_json_mode() {
  local path="$1"
  if [ ! -f "$path" ] || [ -L "$path" ]; then
    printf 'invalid\n'
    return
  fi
  stat -f '%Lp' "$path"
}

timestamp_age_seconds() {
  python3 - "$1" <<'PY'
import datetime
import sys

try:
    value = datetime.datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00"))
    now = datetime.datetime.now(datetime.timezone.utc)
    print(int((now - value).total_seconds()))
except (TypeError, ValueError):
    print(-1)
PY
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

inspect_deployment() {
  local reference="$1"
  local output_file="$2"
  local attempt
  for attempt in 1 2 3; do
    (
      cd "$frontend_root"
      vercel inspect "$reference" --format=json
    ) > "$output_file" || true
    if jq -e 'type == "object" and ((.id // "") | startswith("dpl_"))' \
      "$output_file" >/dev/null 2>&1; then
      return 0
    fi
    if [ "$attempt" -lt 3 ]; then
      sleep 1
    fi
  done
  return 1
}

preview_request() {
  local label="$1"
  local endpoint="$2"
  local result
  local code="000"
  local attempt
  for attempt in 1 2 3; do
    result="$(
      cd "$frontend_root"
      vercel curl "$endpoint" --deployment "${deployment_url#https://}" -- \
        --silent --show-error --max-time 20 \
        --output "$release_dir/$label.body" \
        --dump-header "$release_dir/$label.headers" \
        --write-out '%{http_code}' 2>"$release_dir/$label.error" || true
    )"
    code="$(http_code "$result")"
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

production_request() {
  local label="$1"
  local endpoint="$2"
  shift 2
  local result
  result="$(
    curl --silent --show-error --max-time 20 \
      --output "$release_dir/$label.body" \
      --dump-header "$release_dir/$label.headers" \
      --write-out '%{http_code}' \
      "$@" "$production_origin$endpoint" 2>"$release_dir/$label.error" || true
  )"
  http_code "$result"
}

production_read_request() {
  local label="$1"
  local endpoint="$2"
  shift 2
  local code="000"
  local attempt
  for attempt in 1 2 3 4 5 6; do
    code="$(production_request "$label" "$endpoint" "$@")"
    case "$code" in
      000|5??)
        ;;
      *)
        break
        ;;
    esac
    if [ "$attempt" -lt 6 ]; then
      sleep 1
    fi
  done
  printf '%s\n' "$code"
}

verify_runtime_provenance() {
  local prefix="$1"
  local status_http="$2"
  local expected_id="${3:-$deployment_id}"
  local expected_url="${4:-$deployment_url}"
  if [ "$status_http" != 200 ] ||
    [ "$(header_value "$release_dir/$prefix.headers" X-Qiu-Market-Provenance)" != VERIFIED ] ||
    [ "$(header_value "$release_dir/$prefix.headers" X-Qiu-Market-Deployment-ID)" != "$expected_id" ] ||
    [ "$(header_value "$release_dir/$prefix.headers" X-Qiu-Market-Deployment-URL)" != "$expected_url" ] ||
    [ "$(header_value "$release_dir/$prefix.headers" X-Qiu-Market-Release-Commit)" != "$deployment_commit" ] ||
    ! jq -e '
      .state == "ready" and
      (.last_error // "") == "" and
      ((.outbox_state // "ready") == "ready") and
      ((.outbox_last_error // "") == "")
    ' "$release_dir/$prefix.body" >/dev/null 2>&1; then
    echo "Runtime provenance or trading/outbox readiness does not match the candidate." >&2
    return 1
  fi
}

verify_preview_gate() {
  local mode
  local age
  mode="$(private_json_mode "$gate_report")"
  if [ "$mode" != 600 ] && [ "$mode" != 400 ]; then
    echo "Private Preview gate report is missing or has unsafe permissions." >&2
    return 2
  fi
  if ! jq -e \
    --arg deployment_id "$deployment_id" \
    --arg deployment_url "$deployment_url" \
    --arg deployment_commit "$deployment_commit" '
      .schema_version == 1 and
      .status == "preview-gate-passed" and
      .reason == "all_preview_security_evidence_verified" and
      .deployment.id == $deployment_id and
      .deployment.url == $deployment_url and
      .deployment.commit == $deployment_commit and
      .checks.inspect_ready_preview == true and
      .checks.local_frontend_matches_release == true and
      .checks.vercel_authentication_blocks_ordinary_access == true and
      .checks.protected_spa_deep_links_200 == true and
      .checks.runtime_provenance_matches == true and
      .checks.trading_and_outbox_ready == true and
      .checks.anonymous_session_http == 401 and
      .checks.unsigned_funnel_rest_http == 401 and
      .checks.local_login_disabled == true and
      .checks.oauth_private_configuration_present == true and
      .checks.github_oauth_runtime_capability == true and
      .checks.managed_oauth_close_evidence_verified == true and
      .checks.oauth_browser_evidence_verified == true
    ' "$gate_report" >/dev/null 2>&1; then
    echo "Exact protected Preview Gate 2C has not passed." >&2
    return 2
  fi
  age="$(timestamp_age_seconds "$(jq -r '.checked_at // ""' "$gate_report")")"
  if [ "$age" -lt 0 ] || [ "$age" -gt 900 ]; then
    echo "Preview Gate 2C report is older than 15 minutes; re-run it." >&2
    return 2
  fi
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

verify_pre_promotion_production() {
  local code
  local location
  local state_cookie
  local state_cookie_lower

  code="$(production_read_request baseline-page /)"
  if [ "$code" != 200 ]; then
    echo "Current Production page is not a healthy rollback baseline." >&2
    return 1
  fi
  code="$(
    production_read_request \
      baseline-status \
      /api/v1/trading/markets/BTC-USDT/status
  )"
  if [ "$code" != 200 ] || ! jq -e '
    .state == "ready" and
    (.last_error // "") == "" and
    ((.outbox_state // "ready") == "ready") and
    ((.outbox_last_error // "") == "")
  ' "$release_dir/baseline-status.body" >/dev/null 2>&1; then
    echo "Current Production trading/outbox state is not a healthy rollback baseline." >&2
    return 1
  fi
  code="$(
    production_read_request \
      baseline-capabilities \
      /api/v1/trading/auth/capabilities
  )"
  if [ "$code" != 200 ] || ! jq -e '
    .github_oauth_enabled == true and
    .local_login_enabled == false
  ' "$release_dir/baseline-capabilities.body" >/dev/null 2>&1; then
    echo "Current Production OAuth capability is not restored." >&2
    return 1
  fi
  code="$(
    production_request \
      baseline-oauth-start \
      /api/v1/trading/auth/github/start
  )"
  if [ "$code" != 302 ]; then
    echo "Current Production OAuth start is not a single redirect." >&2
    return 1
  fi
  location="$(header_value "$release_dir/baseline-oauth-start.headers" location)"
  state_cookie="$(
    header_value_containing \
      "$release_dir/baseline-oauth-start.headers" \
      set-cookie \
      s78_trading_oauth_state=
  )"
  state_cookie_lower="$(printf '%s' "$state_cookie" | tr '[:upper:]' '[:lower:]')"
  if ! authorization_redirect_matches \
    "$location" \
    "$production_origin/api/v1/trading/auth/github/callback"; then
    echo "Current Production OAuth redirect_uri is not restored." >&2
    return 1
  fi
  if [[ "$state_cookie" != *s78_trading_oauth_state=* ]] ||
    [[ "$state_cookie_lower" != *secure* ]] ||
    [[ "$state_cookie_lower" != *httponly* ]] ||
    [[ "$state_cookie_lower" != *samesite=lax* ]]; then
    echo "Current Production OAuth state cookie is not secure." >&2
    return 1
  fi
}

read_only_preflight() {
  require_identity
  prepare_private_dir
  require_commands
  if [ -f "$state_file" ]; then
    echo "A Vercel release gate is already active." >&2
    return 1
  fi
  if [ -s "$oauth_window_state" ]; then
    echo "Preview OAuth maintenance window is still active." >&2
    return 1
  fi
  if [ -s "$acceptance_epoch" ] &&
    [ "$(jq -r '.status // ""' "$acceptance_epoch" 2>/dev/null)" = active ]; then
    echo "An acceptance epoch is already active; stop it before promotion." >&2
    return 1
  fi
  verify_preview_gate
  if ! git -C "$repo_root" cat-file -e "$deployment_commit^{commit}" 2>/dev/null ||
    ! git -C "$repo_root" diff --quiet "$deployment_commit" HEAD -- frontend; then
    echo "Current frontend source no longer matches the candidate release commit." >&2
    return 1
  fi

  inspect_deployment "$deployment_url" "$release_dir/candidate-inspect.json"
  if ! jq -e \
    --arg id "$deployment_id" \
    --arg url "${deployment_url#https://}" '
      .id == $id and
      .url == $url and
      .readyState == "READY" and
      (.target == "preview" or .target == "production")
    ' "$release_dir/candidate-inspect.json" >/dev/null 2>&1; then
    echo "Candidate is not the exact READY Preview deployment." >&2
    return 1
  fi

  inspect_deployment "$production_origin" "$release_dir/production-before.json"
  if ! jq -e \
    --arg candidate "$deployment_id" '
      .readyState == "READY" and
      .target == "production" and
      (.id | startswith("dpl_")) and
      .id != $candidate and
      (.url | type == "string") and
      (.url | length > 0)
    ' "$release_dir/production-before.json" >/dev/null 2>&1; then
    echo "Current Production deployment cannot be recorded safely." >&2
    return 1
  fi

  preview_status="$(preview_request preview-status /api/v1/trading/markets/BTC-USDT/status)"
  verify_runtime_provenance preview-status "$preview_status"
  verify_pre_promotion_production
}

load_state() {
  deployment_id="$(jq -r '.candidate.id // ""' "$state_file")"
  deployment_url="$(jq -r '.candidate.url // ""' "$state_file")"
  deployment_commit="$(jq -r '.candidate.commit // ""' "$state_file")"
  promoted_id="$(jq -r '.promoted.id // ""' "$state_file")"
  promoted_url="$(jq -r '.promoted.url // ""' "$state_file")"
  promotion_id="$(jq -r '.promotion_id // ""' "$state_file")"
  previous_id="$(jq -r '.previous.id // ""' "$state_file")"
  previous_url="$(jq -r '.previous.url // ""' "$state_file")"
  promoted_at="$(jq -r '.promoted_at // ""' "$state_file")"
}

write_state() {
  local phase="$1"
  local promotion_id_value="$2"
  local previous_id_value="$3"
  local previous_url_value="$4"
  local promoted_at_value="${5:-}"
  local temp_file
  temp_file="$(mktemp "$release_dir/.state.XXXXXX")"
  jq -n \
    --arg phase "$phase" \
    --arg promotion_id "$promotion_id_value" \
    --arg candidate_id "$deployment_id" \
    --arg candidate_url "$deployment_url" \
    --arg candidate_commit "$deployment_commit" \
    --arg promoted_id "$promoted_id" \
    --arg promoted_url "$promoted_url" \
    --arg previous_id "$previous_id_value" \
    --arg previous_url "$previous_url_value" \
    --arg production_origin "$production_origin" \
    --arg promoted_at "$promoted_at_value" \
    --arg updated_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '
      {
        schema_version: 1,
        phase: $phase,
        promotion_id: $promotion_id,
        candidate: {
          id: $candidate_id,
          url: $candidate_url,
          commit: $candidate_commit
        },
        promoted: {
          id: $promoted_id,
          url: $promoted_url
        },
        previous: {
          id: $previous_id,
          url: $previous_url
        },
        production_origin: $production_origin,
        promoted_at: $promoted_at,
        updated_at: $updated_at
      }
    ' > "$temp_file"
  chmod 600 "$temp_file"
  mv -f "$temp_file" "$state_file"
}

verify_production_alias() {
  local expected_id="$1"
  local attempts="${2:-1}"
  local reference="${3:-$production_origin}"
  local attempt
  for attempt in $(seq 1 "$attempts"); do
    if inspect_deployment \
      "$reference" \
      "$release_dir/production-current.json" 2>/dev/null &&
      jq -e \
        --arg id "$expected_id" '
          .id == $id and
          .readyState == "READY" and
          .target == "production"
        ' "$release_dir/production-current.json" >/dev/null 2>&1; then
      return 0
    fi
    if [ "$attempt" -lt "$attempts" ]; then
      sleep 2
    fi
  done
  return 1
}

validate_promoted_production() {
  local actual_id="$1"
  local actual_url="$2"
  local team_id
  local project_id
  team_id="$(project_identity '.orgId')"
  project_id="$(project_identity '.projectId')"
  (
    cd "$frontend_root"
    vercel api "/v13/deployments/$actual_id" \
      --scope "$team_id" \
      --raw
  ) > "$release_dir/promoted-api.json" 2>/dev/null || true
  jq -e \
    --arg actual_id "$actual_id" \
    --arg actual_url "${actual_url#https://}" \
    --arg candidate_id "$deployment_id" \
    --arg commit "$deployment_commit" \
    --arg project_id "$project_id" '
      .id == $actual_id and
      .url == $actual_url and
      .projectId == $project_id and
      .readyState == "READY" and
      .target == "production" and
      .meta.action == "promote" and
      .meta.originalDeploymentId == $candidate_id and
      .meta.gitCommitSha == $commit and
      .meta.qiuMarketReleaseCommit == $commit
    ' "$release_dir/promoted-api.json" >/dev/null 2>&1
}

snapshot_existing_promoted_ids() {
  local team_id
  local project_id
  team_id="$(project_identity '.orgId')"
  project_id="$(project_identity '.projectId')"
  (
    cd "$frontend_root"
    vercel api "/v9/projects/$project_id" \
      --scope "$team_id" \
      --raw
  ) > "$release_dir/project-before-promotion.json"
  jq -e '
      (.latestDeployments | type == "array") and
      all(.latestDeployments[]; (.id | type == "string"))
    ' "$release_dir/project-before-promotion.json" >/dev/null
  jq '[.latestDeployments[].id] | unique' \
    "$release_dir/project-before-promotion.json" \
    > "$release_dir/promoted-ids-before.json"
}

resolve_latest_promoted_production() {
  local team_id
  local project_id
  local result
  local attempt
  local earliest_created_at
  team_id="$(project_identity '.orgId')"
  project_id="$(project_identity '.projectId')"
  if [[ ! "$promotion_started_at_ms" =~ ^[0-9]+$ ]] ||
    [ "$promotion_started_at_ms" -le 0 ]; then
    echo "Promotion start time is unavailable; refusing ambiguous clone discovery." >&2
    return 1
  fi
  if ! jq -e 'type == "array" and all(.[]; type == "string")' \
    "$release_dir/promoted-ids-before.json" >/dev/null 2>&1; then
    echo "Pre-promotion deployment identity snapshot is unavailable." >&2
    return 1
  fi
  earliest_created_at=$((promotion_started_at_ms - 5000))
  if [ "$earliest_created_at" -lt 0 ]; then
    earliest_created_at=0
  fi
  for attempt in $(seq 1 "$promotion_resolve_attempts"); do
    (
      cd "$frontend_root"
      vercel api "/v9/projects/$project_id" \
        --scope "$team_id" \
        --raw
    ) > "$release_dir/project-api.json" 2>/dev/null || true
    result="$(
      jq -er \
        --arg candidate_id "$deployment_id" \
        --arg commit "$deployment_commit" \
        --argjson earliest_created_at "$earliest_created_at" \
        --slurpfile prior "$release_dir/promoted-ids-before.json" '
          [
            .latestDeployments[] |
            select(
              .id as $id |
              .readyState == "READY" and
              .target == "production" and
              .meta.action == "promote" and
              .meta.originalDeploymentId == $candidate_id and
              .meta.gitCommitSha == $commit and
              .meta.qiuMarketReleaseCommit == $commit and
              (.createdAt | type == "number") and
              .createdAt >= $earliest_created_at and
              ($prior[0] | index($id)) == null
            )
          ] |
          max_by(.createdAt) |
          [.id, ("https://" + .url)] |
          @tsv
        ' "$release_dir/project-api.json" 2>/dev/null
    )" || result=""
    if [ -n "$result" ]; then
      promoted_id="${result%%$'\t'*}"
      promoted_url="${result#*$'\t'}"
      if validate_promoted_production "$promoted_id" "$promoted_url"; then
        return 0
      fi
    fi
    if [ "$attempt" -lt "$promotion_resolve_attempts" ]; then
      sleep "$promotion_resolve_interval"
    fi
  done
  echo "Fresh promoted Production clone was not discoverable after promotion." >&2
  return 1
}

load_production_aliases() {
  local team_id
  local project_id
  team_id="$(project_identity '.orgId')"
  project_id="$(project_identity '.projectId')"
  (
    cd "$frontend_root"
    vercel api "/v9/projects/$project_id" \
      --scope "$team_id" \
      --raw
  ) > "$release_dir/project-aliases-api.json" 2>/dev/null || true
  jq -er '.targets.production.alias[]' \
    "$release_dir/project-aliases-api.json" > "$release_dir/production-aliases.txt"
  if ! grep -Fxq "${production_origin#https://}" "$release_dir/production-aliases.txt" ||
    [ "$(wc -l < "$release_dir/production-aliases.txt" | tr -d ' ')" -gt 10 ] ||
    grep -Ev '^[A-Za-z0-9.-]+\.vercel\.app$' \
      "$release_dir/production-aliases.txt" >/dev/null; then
    echo "Production alias set is unsafe or incomplete." >&2
    return 1
  fi
}

set_production_aliases() {
  local deployment="$1"
  local alias
  while IFS= read -r alias; do
    (
      cd "$frontend_root"
      vercel alias set "$deployment" "$alias"
    )
  done < "$release_dir/production-aliases.txt"
}

verify_production_aliases() {
  local expected_id="$1"
  local alias
  while IFS= read -r alias; do
    if ! verify_production_alias "$expected_id" 10 "$alias"; then
      return 1
    fi
  done < "$release_dir/production-aliases.txt"
}

verify_production_smoke() {
  local page
  local code
  local result
  local attempt
  for page in / /markets /trade/BTC-USDT /system; do
    code="$(production_read_request "page-$(printf '%s' "$page" | tr '/-' '__')" "$page")"
    if [ "$code" != 200 ]; then
      echo "Production page failed after promotion: $page HTTP $code" >&2
      return 1
    fi
  done

  code="$(
    production_read_request \
      production-status \
      /api/v1/trading/markets/BTC-USDT/status
  )"
  if ! verify_runtime_provenance \
    production-status \
    "$code" \
    "$promoted_id" \
    "$promoted_url"; then
    return 1
  fi

  code="$(
    production_read_request \
      production-capabilities \
      /api/v1/trading/auth/capabilities
  )"
  if [ "$code" != 200 ] || ! jq -e '
    .github_oauth_enabled == true and
    .local_login_enabled == false
  ' "$release_dir/production-capabilities.body" >/dev/null 2>&1; then
    echo "Production OAuth capability is not ready after promotion." >&2
    return 1
  fi

  code="$(production_read_request production-session /api/v1/trading/session)"
  if [ "$code" != 401 ]; then
    echo "Anonymous Production session must remain 401; got $code." >&2
    return 1
  fi

  code="000"
  for attempt in 1 2 3; do
    result="$(
      curl --silent --show-error --max-time 20 \
        --output "$release_dir/unsigned-funnel.body" \
        --dump-header "$release_dir/unsigned-funnel.headers" \
        --write-out '%{http_code}' \
        --request POST \
        --header 'content-type: application/json' \
        --data '{"consumer_token":"promotion-gate","venue":"all","universe":"provider_union"}' \
        "$funnel_origin/api/v2/get_market_overview" \
        2>"$release_dir/unsigned-funnel.error" || true
    )"
    code="$(http_code "$result")"
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
  if [ "$code" != 401 ]; then
    echo "Unsigned Funnel REST is no longer rejected after promotion." >&2
    return 1
  fi
}

wait_for_production_smoke() {
  local attempt
  if [[ ! "$promotion_smoke_attempts" =~ ^[1-9][0-9]*$ ]] ||
    [[ ! "$promotion_smoke_interval" =~ ^[0-9]+$ ]]; then
    echo "Promotion smoke retry settings must be non-negative integers." >&2
    return 1
  fi
  for attempt in $(seq 1 "$promotion_smoke_attempts"); do
    if verify_production_smoke; then
      return 0
    fi
    if [ "$attempt" -lt "$promotion_smoke_attempts" ]; then
      sleep "$promotion_smoke_interval"
    fi
  done
  return 1
}

write_last_report() {
  local status="$1"
  local rollback_verified="$2"
  local reason="${3:-}"
  local temp_file
  temp_file="$(mktemp "$release_dir/.last.XXXXXX")"
  jq -n \
    --arg status "$status" \
    --arg promotion_id "${promotion_id:-}" \
    --arg candidate_id "$deployment_id" \
    --arg candidate_url "$deployment_url" \
    --arg candidate_commit "$deployment_commit" \
    --arg promoted_id "${promoted_id:-}" \
    --arg promoted_url "${promoted_url:-}" \
    --arg previous_id "${previous_id:-}" \
    --arg previous_url "${previous_url:-}" \
    --arg rollback_verified "$rollback_verified" \
    --arg reason "$reason" \
    --arg completed_at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '
      {
        schema_version: 1,
        status: $status,
        promotion_id: $promotion_id,
        candidate: {
          id: $candidate_id,
          url: $candidate_url,
          commit: $candidate_commit
        },
        promoted: {
          id: $promoted_id,
          url: $promoted_url
        },
        previous: {
          id: $previous_id,
          url: $previous_url
        },
        rollback_verified: ($rollback_verified == "true"),
        reason: $reason,
        completed_at: $completed_at
      }
    ' > "$temp_file"
  chmod 600 "$temp_file"
  mv -f "$temp_file" "$last_report"
}

rollback_previous() {
  set_production_aliases "$previous_url"
  verify_production_aliases "$previous_id"
}

reject_production_acceptance() {
  local status="$1"
  local reason="$2"
  local exit_code="$3"
  echo "$reason; rolling back the candidate Production deployment." >&2
  if rollback_previous; then
    write_last_report "$status" true "$reason"
    rm -f "$state_file"
  else
    write_state \
      rollback_failed \
      "$promotion_id" \
      "$previous_id" \
      "$previous_url" \
      "$promoted_at"
    echo "Production acceptance rollback could not be verified; state is preserved." >&2
  fi
  exit "$exit_code"
}

handle_promotion_failure() {
  local exit_code="$?"
  trap - EXIT HUP INT TERM
  if [ "$promotion_may_be_applied" = true ]; then
    echo "Promotion did not reach a verified structural gate; rolling back." >&2
    if rollback_previous; then
      write_last_report \
        rolled_back_after_failed_promotion \
        true \
        structural_post_promotion_check_failed
      rm -f "$state_file"
    else
      write_state rollback_failed "$promotion_id" "$previous_id" "$previous_url" "${promoted_at:-}"
      echo "Automatic rollback could not be verified; release state is preserved." >&2
    fi
  fi
  release_lock
  exit "$exit_code"
}

case "$action" in
  status)
    prepare_private_dir
    if [ -f "$state_file" ]; then
      jq '.' "$state_file"
    elif [ -f "$last_report" ]; then
      jq '.' "$last_report"
    else
      jq -n '{schema_version: 1, status: "never-promoted"}'
    fi
    ;;
  preflight)
    read_only_preflight
    jq -n \
      --arg deployment_id "$deployment_id" \
      --arg deployment_url "$deployment_url" \
      --arg deployment_commit "$deployment_commit" \
      --arg previous_id "$(jq -r '.id' "$release_dir/production-before.json")" '
        {
          schema_version: 1,
          status: "promotion-preflight-passed",
          candidate: {
            id: $deployment_id,
            url: $deployment_url,
            commit: $deployment_commit
          },
          previous_production_id: $previous_id,
          mutated: false
        }
      '
    ;;
  promote)
    if [ "$execute_promotion" != true ]; then
      echo "Promotion requires the explicit --execute flag." >&2
      exit 2
    fi
    acquire_lock
    trap release_lock EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    read_only_preflight
    load_production_aliases
    previous_id="$(jq -r '.id' "$release_dir/production-before.json")"
    previous_url="https://$(jq -r '.url' "$release_dir/production-before.json")"
    if [ -n "$promoted_id" ]; then
      validate_promoted_production "$promoted_id" "$promoted_url"
    fi
    promotion_id="$(openssl rand -hex 16)"
    if [ -z "$promoted_id" ]; then
      snapshot_existing_promoted_ids
    fi
    write_state promoting "$promotion_id" "$previous_id" "$previous_url"

    promotion_may_be_applied=true
    trap handle_promotion_failure EXIT
    if [ -z "$promoted_id" ]; then
      promotion_started_at_ms="$(python3 - <<'PY'
import time
print(time.time_ns() // 1_000_000)
PY
)"
      (
        cd "$frontend_root"
        vercel promote "$deployment_url" --yes --timeout 3m
      )
      resolve_latest_promoted_production
    fi
    set_production_aliases "$promoted_url"
    verify_production_aliases "$promoted_id"
    wait_for_production_smoke
    promoted_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    write_state \
      awaiting-production-auth \
      "$promotion_id" \
      "$previous_id" \
      "$previous_url" \
      "$promoted_at"

    promotion_may_be_applied=false
    trap - EXIT HUP INT TERM
    release_lock
    jq '.' "$state_file"
    ;;
  confirm)
    acquire_lock
    trap release_lock EXIT
    if [ ! -f "$state_file" ]; then
      echo "No promoted release is awaiting Production acceptance." >&2
      exit 1
    fi
    load_state
    if [ "$(jq -r '.phase // ""' "$state_file")" != awaiting-production-auth ]; then
      echo "Active Vercel release is not ready for Production confirmation." >&2
      exit 1
    fi
    require_identity
    require_commands
    if [[ ! "$promoted_id" =~ ^dpl_[A-Za-z0-9]+$ ]] ||
      [[ ! "$promoted_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]]; then
      echo "Promoted Production identity is missing from release state." >&2
      exit 1
    fi
    if ! verify_production_aliases "$promoted_id"; then
      echo "One or more Production aliases no longer point to the promoted deployment." >&2
      exit 1
    fi
    if ! verify_production_smoke; then
      reject_production_acceptance \
        rolled_back_after_production_smoke_failure \
        production_smoke_failed_during_confirmation \
        1
    fi

    evidence_mode="$(private_json_mode "$production_evidence")"
    evidence_completed_at=""
    if { [ "$evidence_mode" != 600 ] && [ "$evidence_mode" != 400 ]; } ||
      ! jq -e \
        --arg promotion_id "$promotion_id" \
        --arg deployment_id "$deployment_id" \
        --arg deployment_commit "$deployment_commit" \
        --arg promoted_id "$promoted_id" \
        --arg promoted_at "$promoted_at" '
          .schema_version == 1 and
          .promotion_id == $promotion_id and
          .deployment_id == $deployment_id and
          .deployment_commit == $deployment_commit and
          .production_deployment_id == $promoted_id and
          .production_login == true and
          .github_login == "qianqiu0404" and
          .secure_cookie == true and
          .csrf_rejected == true and
          .origin_rejected == true and
          .minimal_virtual_write_reconciled == true and
          ((.request_id // "") | test("^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$")) and
          .same_request_id_replay_equal == true and
          .ledger_balanced == true and
          .state_hash_consistent == true and
          .production_logout_204 == true and
          .stale_production_session_401 == true and
          (
            (.completed_at) as $completed |
            try (
              ($completed | fromdateiso8601) >=
              ($promoted_at | fromdateiso8601)
            )
            catch false
          )
      ' "$production_evidence" >/dev/null 2>&1; then
      reject_production_acceptance \
        rolled_back_after_production_evidence_failure \
        production_auth_write_evidence_missing_or_mismatched \
        2
    fi
    evidence_completed_at="$(jq -r '.completed_at // ""' "$production_evidence")"
    evidence_age="$(timestamp_age_seconds "$evidence_completed_at")"
    if [ "$evidence_age" -lt 0 ] || [ "$evidence_age" -gt 900 ]; then
      reject_production_acceptance \
        rolled_back_after_production_evidence_failure \
        production_auth_write_evidence_outside_15_minute_window \
        2
    fi
    write_last_report \
      production-gate-passed \
      false \
      production_auth_and_minimal_write_verified
    rm -f "$state_file"
    jq '.' "$last_report"
    ;;
  rollback)
    acquire_lock
    trap release_lock EXIT
    if [ ! -f "$state_file" ]; then
      echo "No active Vercel release exists to roll back." >&2
      exit 1
    fi
    load_state
    require_identity
    require_commands
    rollback_previous
    write_last_report manually-rolled-back true operator_requested_rollback
    rm -f "$state_file"
    jq '.' "$last_report"
    ;;
  *)
    usage
    ;;
esac
