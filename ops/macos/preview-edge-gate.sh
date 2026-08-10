#!/usr/bin/env bash
set -euo pipefail

# This helper owns only the small, non-secret readiness contract shared by a
# preview stack, its loopback front door, and its tunnel. The caller owns the
# actual processes. Secrets are read from private files and are never passed in
# argv, written to evidence, or included in diagnostics.

action="${1:-status}"
candidate="${2:-}"
runtime_dir="${QIU_PREVIEW_EDGE_DIR:-}"
max_age_seconds="${QIU_PREVIEW_GENERATION_MAX_AGE_SECONDS:-15}"
expected_commit="${QIU_PREVIEW_EXPECTED_COMMIT:-}"
deployment_origin="${QIU_PREVIEW_DEPLOYMENT_ORIGIN:-}"
deployment_id="${QIU_PREVIEW_DEPLOYMENT_ID:-}"
project_id="${QIU_PREVIEW_PROJECT_ID:-}"
deployment_attestation_file="${QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE:-}"
bypass_file="${QIU_PREVIEW_PROTECTION_BYPASS_FILE:-}"
quality_status="${QIU_PREVIEW_EXPECTED_QUALITY_STATUS:-unconfigured}"

fail() {
  printf 'preview edge gate failed: %s\n' "$1" >&2
  exit 1
}

require_private_dir() {
  local path="$1"
  [ -d "$path" ] && [ ! -L "$path" ] || fail 'runtime directory is unavailable'
  [ "$(stat -f '%u:%Lp' "$path")" = "$(id -u):700" ] || fail 'runtime directory ACL is unsafe'
}

require_private_file() {
  local path="$1"
  [ -f "$path" ] && [ ! -L "$path" ] || fail 'private file is unavailable'
  [ "$(stat -f '%u:%Lp' "$path")" = "$(id -u):600" ] || fail 'private file ACL is unsafe'
}

require_runtime() {
  [ -n "$runtime_dir" ] || fail 'QIU_PREVIEW_EDGE_DIR is required'
  [[ "$runtime_dir" = /* ]] || fail 'runtime directory must be absolute'
  require_private_dir "$runtime_dir"
  install -d -m 700 "$runtime_dir/run"
  [ ! -L "$runtime_dir/run" ] || fail 'runtime run directory is unsafe'
  [ "$(stat -f '%u:%Lp' "$runtime_dir/run")" = "$(id -u):700" ] || fail 'runtime run directory ACL is unsafe'
}

require_expected_commit() {
  [[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || fail 'QIU_PREVIEW_EXPECTED_COMMIT must be an exact lowercase 40-hex commit'
}

manifest_path() { printf '%s/run/committed-generation.json\n' "$runtime_dir"; }
drain_path() { printf '%s/run/drain.json\n' "$runtime_dir"; }

validate_generation_shape() {
  local path="$1"
  local now
  require_private_file "$path"
  require_expected_commit
  [ "$(stat -f '%z' "$path")" -le 16384 ] || fail 'generation manifest is oversized'
  now="$(date '+%s')"
  jq -e \
    --arg commit "$expected_commit" \
    --argjson now "$now" \
    --argjson max_age "$max_age_seconds" '
      type == "object" and
      .schema_version == "qiu.preview-edge.generation.v1" and
      .committed == true and
      (.generation | type == "string" and test("^[a-zA-Z0-9._:-]{1,128}$")) and
      (.source_commit | type == "string" and test("^[0-9a-f]{40}$")) and
      .source_commit == $commit and
      (.ready_epoch as $ready |
        .verified_epoch as $verified |
        ($ready | type == "number" and floor == . and . > 0) and
        ($verified | type == "number" and floor == . and . >= $ready) and
        $verified <= ($now + 5) and
        ($now - $verified) <= $max_age
      ) and
      .quick_tunnel.ephemeral == true and
      (.quick_tunnel.expected_hostname | type == "string" and test("^[a-z0-9][a-z0-9.-]*\\.trycloudflare\\.com$")) and
      (.components | type == "array" and length == 6) and
      ([.components[].name] | sort) == ["api","authority_postgres","frontdoor","redis","rpc","trading"] and
      ([.components[].pid] | unique | length) == 6 and
      ([.components[].port] | unique | length) == 6 and
      ([.components[] | {key:.name,value:.port}] | from_entries) == {
        api:18080,
        authority_postgres:5432,
        frontdoor:18084,
        redis:6389,
        rpc:18083,
        trading:18081
      } and
      all(.components[];
        (.pid | type == "number" and floor == . and . > 1) and
        (.port | type == "number" and floor == . and . >= 1 and . <= 65535) and
        (.started_epoch | type == "number" and floor == . and . > 0) and
        (.binary_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
      )
    ' "$path" >/dev/null || fail 'generation manifest failed schema or freshness validation'
}

validate_generation_processes() {
  local path="$1"
  local name pid port started_epoch binary_sha owner listener listener_count executable live_started live_sha
  while IFS='|' read -r name pid port started_epoch binary_sha; do
    kill -0 "$pid" 2>/dev/null || fail "$name process is not running"
    owner="$(lsof -nP -a -p "$pid" -iTCP:"$port" -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true)"
    [ "$owner" = "$pid" ] || fail "$name listener does not match committed generation"
    listener_count=0
    while IFS= read -r listener; do
      case "$listener" in
        "127.0.0.1:$port"|"[::1]:$port") ;;
        *) fail "$name listener is outside exact loopback" ;;
      esac
      listener_count=$((listener_count + 1))
    done < <(lsof -nP -a -p "$pid" -iTCP:"$port" -sTCP:LISTEN -Fn 2>/dev/null | awk '/^n/{sub(/^n/,"");print}' || true)
    [ "$listener_count" -gt 0 ] || fail "$name has no exact loopback listener"
    executable="$(lsof -nP -a -p "$pid" -d txt -Fn 2>/dev/null | awk '/^n/{sub(/^n/,"");print;exit}' || true)"
    [ -n "$executable" ] && [ -f "$executable" ] && [ ! -L "$executable" ] || fail "$name executable identity is unavailable"
    live_sha="$(shasum -a 256 "$executable" | awk '{print $1}')"
    [ "$live_sha" = "$binary_sha" ] || fail "$name executable digest drifted"
    live_started="$(ps -p "$pid" -o lstart= | awk '{$1=$1;print}')"
    [ -n "$live_started" ] || fail "$name process start time is unavailable"
    live_started="$(LC_ALL=C date -j -f '%a %b %e %T %Y' "$live_started" '+%s' 2>/dev/null || true)"
    [ "$live_started" = "$started_epoch" ] || fail "$name process start time drifted"
  done < <(jq -r '.components[] | [.name,.pid,.port,.started_epoch,.binary_sha256] | join("|")' "$path")
}

validate_committed() {
  local path
  path="$(manifest_path)"
  validate_generation_shape "$path"
  validate_generation_processes "$path"
}

validate_origin() {
  local hostname
  [ -n "$deployment_origin" ] || fail 'QIU_PREVIEW_DEPLOYMENT_ORIGIN is required'
  [[ "$deployment_origin" =~ ^https://[a-z0-9][a-z0-9.-]*\.vercel\.app$ ]] || fail 'deployment origin must be exact HTTPS vercel.app origin'
  [[ "$deployment_origin" != 'https://qiu-market.vercel.app' ]] || fail 'production alias is not an immutable preview origin'
  [ -n "$deployment_id" ] && [[ "$deployment_id" =~ ^dpl_[A-Za-z0-9]{20,}$ ]] || fail 'QIU_PREVIEW_DEPLOYMENT_ID is invalid'
  [ -n "$project_id" ] && [[ "$project_id" =~ ^prj_[A-Za-z0-9]{20,}$ ]] || fail 'QIU_PREVIEW_PROJECT_ID is invalid'
  require_expected_commit
  hostname="${deployment_origin#https://}"
  [ "$hostname" != 'qiu-market.vercel.app' ] || fail 'production alias is forbidden'
}

validate_deployment_attestation() {
  require_private_file "$deployment_attestation_file"
  [ "$(stat -f '%z' "$deployment_attestation_file")" -le 8192 ] || fail 'deployment attestation is oversized'
  jq -e \
    --arg project "$project_id" \
    --arg deployment "$deployment_id" \
    --arg origin "$deployment_origin" \
    --arg commit "$expected_commit" '
      type == "object" and
      (keys | sort) == ["attested_at","deployment_id","git_commit","immutable_url","project_id","release_commit","schema_version","source_commit","state","target"] and
      .schema_version == "qiu.preview-edge.deployment-attestation.v1" and
      .project_id == $project and
      .deployment_id == $deployment and
      .immutable_url == $origin and
      .state == "READY" and
      .target == null and
      .git_commit == $commit and
      .source_commit == $commit and
      .release_commit == $commit and
      (.attested_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
    ' "$deployment_attestation_file" >/dev/null || fail 'deployment attestation does not match the expected Preview release'
}

protected_request() (
  local route="$1"
  local expected_status="$2"
  local body headers status content_type cache_control bypass response_deployment response_url response_commit response_provenance
  case "$route" in
    /api/v1/trading/auth/capabilities|/api/v1/data-quality/summary) ;;
    *) fail 'protected probe path is not allowlisted' ;;
  esac
  require_private_file "$bypass_file"
  bypass="$(<"$bypass_file")"
  [[ "$bypass" =~ ^[A-Za-z0-9_-]+$ ]] || fail 'protection bypass format is invalid'
  [ "${#bypass}" -ge 16 ] && [ "${#bypass}" -le 128 ] || fail 'protection bypass length is invalid'
  body="$(mktemp "$runtime_dir/run/protected-body.XXXXXX")"
  headers="$(mktemp "$runtime_dir/run/protected-headers.XXXXXX")"
  chmod 600 "$body" "$headers"
  cleanup_probe() { find "$body" "$headers" -maxdepth 0 -type f -delete 2>/dev/null || true; }
  trap cleanup_probe EXIT INT TERM HUP
  status="$(curl \
    --config <(printf 'header = "x-vercel-protection-bypass: %s"\n' "$bypass") \
    --silent --show-error --max-time 15 \
    --dump-header "$headers" --output "$body" --write-out '%{http_code}' \
    "$deployment_origin$route")" || fail 'protected Preview request failed'
  [ "$status" = "$expected_status" ] || fail 'protected Preview returned unexpected status'
  content_type="$(awk 'tolower($0) ~ /^content-type:/{gsub(/\r/,"");print tolower($2);exit}' "$headers")"
  cache_control="$(awk 'tolower($0) ~ /^cache-control:/{sub(/^[^:]+:[[:space:]]*/,"");gsub(/\r/,"");print tolower($0);exit}' "$headers")"
  response_deployment="$(awk 'tolower($0) ~ /^x-qiu-market-deployment-id:/{sub(/^[^:]+:[[:space:]]*/,"");gsub(/\r/,"");print;exit}' "$headers")"
  response_url="$(awk 'tolower($0) ~ /^x-qiu-market-deployment-url:/{sub(/^[^:]+:[[:space:]]*/,"");gsub(/\r/,"");print;exit}' "$headers")"
  response_commit="$(awk 'tolower($0) ~ /^x-qiu-market-release-commit:/{sub(/^[^:]+:[[:space:]]*/,"");gsub(/\r/,"");print;exit}' "$headers")"
  response_provenance="$(awk 'tolower($0) ~ /^x-qiu-market-provenance:/{sub(/^[^:]+:[[:space:]]*/,"");gsub(/\r/,"");print;exit}' "$headers")"
  [[ "$content_type" = application/json* ]] || fail 'protected Preview returned unexpected content type'
  [[ ",$cache_control," = *,no-store,* || "$cache_control" = *no-store* ]] || fail 'protected Preview response is cacheable'
  [ "$response_deployment" = "$deployment_id" ] || fail 'protected Preview deployment ID drifted'
  [ "$response_url" = "$deployment_origin" ] || fail 'protected Preview deployment URL drifted'
  [ "$response_commit" = "$expected_commit" ] || fail 'protected Preview release commit drifted'
  [ "$response_provenance" = VERIFIED ] || fail 'protected Preview provenance is not verified'
  if [ "$expected_status" = 200 ]; then
    case "$route" in
      /api/v1/trading/auth/capabilities)
        jq -e '.local_login_enabled == true and .recovery_gate_enabled == false and .github_oauth_enabled == false' "$body" >/dev/null || fail 'trading capabilities schema mismatch'
        ;;
      /api/v1/data-quality/summary)
        jq -e --arg status "$quality_status" '.schemaVersion == "data-quality/v1" and .status == $status and (.items | type == "array" and length == 3)' "$body" >/dev/null || fail 'data quality schema mismatch'
        ;;
    esac
  else
    jq -e '.error.code == "preview_not_ready" and .retryable == true' "$body" >/dev/null || fail 'drain response schema mismatch'
  fi
  printf 'protected_path=%s status=%s cache=no-store\n' "$route" "$status"
)

case "$action" in
  commit)
    require_runtime
    [ -n "$candidate" ] || fail 'candidate manifest path is required'
    validate_generation_shape "$candidate"
    validate_generation_processes "$candidate"
    destination="$(manifest_path)"
    temporary="$(mktemp "$runtime_dir/run/committed-generation.XXXXXX")"
    install -m 600 "$candidate" "$temporary"
    mv "$temporary" "$destination"
    printf 'generation_committed=%s\n' "$(jq -r '.generation' "$destination")"
    ;;
  check)
    require_runtime
    [ ! -e "$(drain_path)" ] || fail 'preview edge is draining'
    validate_committed
    printf 'generation_ready=%s\n' "$(jq -r '.generation' "$(manifest_path)")"
    ;;
  drain)
    require_runtime
    reason="${candidate:-planned_restart}"
    case "$reason" in planned_restart|shutdown|fault) ;; *) fail 'invalid drain reason' ;; esac
    temporary="$(mktemp "$runtime_dir/run/drain.XXXXXX")"
    jq -n --arg reason "$reason" --argjson started_epoch "$(date '+%s')" '{schema_version:"qiu.preview-edge.drain.v1",reason:$reason,started_epoch:$started_epoch}' > "$temporary"
    chmod 600 "$temporary"
    mv "$temporary" "$(drain_path)"
    find "$(manifest_path)" -maxdepth 0 -type f -delete 2>/dev/null || true
    printf 'preview_edge_draining=true reason=%s\n' "$reason"
    ;;
  resume)
    require_runtime
    validate_committed
    find "$(drain_path)" -maxdepth 0 -type f -delete 2>/dev/null || true
    printf 'preview_edge_draining=false generation=%s\n' "$(jq -r '.generation' "$(manifest_path)")"
    ;;
  clear)
    require_runtime
    find "$(manifest_path)" -maxdepth 0 -type f -delete 2>/dev/null || true
    printf 'generation_cleared=true\n'
    ;;
  probe)
    require_runtime
    validate_origin
    validate_deployment_attestation
    [ ! -e "$(drain_path)" ] || fail 'preview edge is draining'
    validate_committed
    protected_request /api/v1/trading/auth/capabilities 200
    protected_request /api/v1/data-quality/summary 200
    ;;
  probe-drain)
    require_runtime
    validate_origin
    validate_deployment_attestation
    require_private_file "$(drain_path)"
    protected_request /api/v1/trading/auth/capabilities 503
    protected_request /api/v1/data-quality/summary 503
    ;;
  status)
    require_runtime
    if [ -e "$(drain_path)" ]; then
      printf 'preview_edge_status=draining\n'
    elif (validate_committed) >/dev/null 2>&1; then
      printf 'preview_edge_status=ready generation=%s\n' "$(jq -r '.generation' "$(manifest_path)")"
    else
      printf 'preview_edge_status=not_ready\n'
    fi
    ;;
  *)
    printf 'Usage: %s commit <candidate.json>|check|drain [planned_restart|shutdown|fault]|resume|clear|probe|probe-drain|status\n' "$0" >&2
    exit 2
    ;;
esac
