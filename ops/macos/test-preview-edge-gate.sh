#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
gate="$repo_root/ops/macos/preview-edge-gate.sh"
fixture="$(mktemp -d /tmp/qiu-preview-edge.XXXXXX)"
fixture_pids=()
cleanup() {
  for pid in "${fixture_pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait "${fixture_pids[@]}" 2>/dev/null || true
  find "$fixture" -depth -delete
}
trap cleanup EXIT

runtime="$fixture/runtime"
fixture_bin="$fixture/bin"
mkdir -p "$runtime/run" "$runtime/secrets" "$fixture_bin"
chmod 700 "$runtime" "$runtime/run" "$runtime/secrets" "$fixture_bin"

cat > "$fixture_bin/lsof" <<'SH'
#!/usr/bin/env bash
port=''
pid=''
field=''
descriptor=''
for argument in "$@"; do
  case "$argument" in
    -iTCP:*) port="${argument#-iTCP:}" ;;
    -p) next_is_pid=true ;;
    -d) next_is_descriptor=true ;;
    -Fp|-Fn) field="$argument" ;;
    *)
      if [ "${next_is_pid:-}" = true ]; then pid="$argument"; next_is_pid=false; fi
      if [ "${next_is_descriptor:-}" = true ]; then descriptor="$argument"; next_is_descriptor=false; fi
      ;;
  esac
done
if [ "$descriptor" = txt ]; then
  [ "$field" = -Fn ] && printf 'n/bin/sleep\n'
  exit 0
fi
if [ "${QIU_FIXTURE_LISTENER_DRIFT:-}" = "$port" ]; then
  printf 'p999999\n'
elif ! awk -F'|' -v pid="$pid" -v port="$port" '$1==pid && $2==port{found=1} END{exit !found}' "$QIU_FIXTURE_PROCESS_MAP"; then
  exit 0
elif [ "$field" = -Fp ]; then
  printf 'p%s\n' "$pid"
else
  if [ "${QIU_FIXTURE_DUAL_LOOPBACK_PORT:-}" = "$port" ]; then
    printf 'n[::1]:%s\n' "$port"
  fi
  printf 'n127.0.0.1:%s\n' "$port"
  if [ "${QIU_FIXTURE_WILDCARD_PORT:-}" = "$port" ]; then
    printf 'n*:%s\n' "$port"
  fi
fi
SH
cat > "$fixture_bin/ps" <<'SH'
#!/usr/bin/env bash
pid=''
while [ "$#" -gt 0 ]; do
  case "$1" in -p) pid="$2"; shift 2 ;; *) shift ;; esac
done
awk -F'|' -v pid="$pid" '$1==pid{print ENVIRON["QIU_FIXTURE_PROCESS_START"]; found=1} END{exit !found}' "$QIU_FIXTURE_PROCESS_MAP"
SH
cat > "$fixture_bin/curl" <<'SH'
#!/usr/bin/env bash
headers=''
body=''
url=''
config=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --config) config="$2"; shift 2 ;;
    --dump-header) headers="$2"; shift 2 ;;
    --output) body="$2"; shift 2 ;;
    http*) url="$1"; shift ;;
    *) shift ;;
  esac
done
[ -r "$config" ] || exit 23
grep -Eq '^header = "x-vercel-protection-bypass: [A-Za-z0-9_-]+"$' "$config" || exit 24
printf 'header_present=true url=%s\n' "$url" >> "$QIU_FIXTURE_CURL_ARGV_LOG"
if [ "$QIU_FIXTURE_DRAIN" = true ]; then
  status=503
  payload='{"error":{"code":"preview_not_ready"},"retryable":true}'
else
  status=200
  case "$url" in
    */api/v1/trading/auth/capabilities)
      payload='{"github_oauth_enabled":false,"local_login_enabled":true,"recovery_gate_enabled":false}'
      ;;
    */api/v1/data-quality/summary)
      payload='{"schemaVersion":"data-quality/v1","status":"unconfigured","items":[{},{},{}]}'
      ;;
    *) exit 22 ;;
  esac
fi
printf 'HTTP/2 %s\r\nContent-Type: application/json; charset=utf-8\r\nCache-Control: no-store\r\nX-Qiu-Market-Deployment-ID: %s\r\nX-Qiu-Market-Deployment-URL: https://qiu-market-fixture-qianqiu0404s-projects.vercel.app\r\nX-Qiu-Market-Release-Commit: 4b2a278d77b0f6aaae5c5ea103db1e65ed1d2441\r\nX-Qiu-Market-Provenance: VERIFIED\r\n\r\n' "$status" "${QIU_FIXTURE_DEPLOYMENT_ID:-dpl_12345678901234567890}" > "$headers"
printf '%s\n' "$payload" > "$body"
printf '%s' "$status"
SH
chmod 700 "$fixture_bin/lsof" "$fixture_bin/ps" "$fixture_bin/curl"

export PATH="$fixture_bin:/usr/bin:/bin:/usr/sbin:/sbin"
process_map="$fixture/process-map"
: > "$process_map"
for port in 5432 6389 18081 18083 18080 18084; do
  /bin/sleep 600 &
  fixture_pids+=("$!")
  printf '%s|%s\n' "$!" "$port" >> "$process_map"
done
chmod 600 "$process_map"
export QIU_FIXTURE_PROCESS_MAP="$process_map"
export QIU_FIXTURE_LISTENER_DRIFT=''
export QIU_FIXTURE_DUAL_LOOPBACK_PORT=5432
export QIU_FIXTURE_WILDCARD_PORT=''
export QIU_FIXTURE_DRAIN=false
export QIU_FIXTURE_CURL_ARGV_LOG="$fixture/curl-argv.log"
export QIU_FIXTURE_DEPLOYMENT_ID=dpl_12345678901234567890
export QIU_PREVIEW_EDGE_DIR="$runtime"
export QIU_PREVIEW_GENERATION_MAX_AGE_SECONDS=15
export QIU_PREVIEW_EXPECTED_COMMIT=4b2a278d77b0f6aaae5c5ea103db1e65ed1d2441
export QIU_PREVIEW_DEPLOYMENT_ORIGIN=https://qiu-market-fixture-qianqiu0404s-projects.vercel.app
export QIU_PREVIEW_DEPLOYMENT_ID=dpl_12345678901234567890
export QIU_PREVIEW_PROJECT_ID=prj_12345678901234567890
export QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE="$runtime/deployment-attestation.json"
export QIU_PREVIEW_PROTECTION_BYPASS_FILE="$runtime/secrets/vercel-automation-bypass"
export QIU_PREVIEW_EXPECTED_QUALITY_STATUS=unconfigured

printf '%s\n' 'fixture_bypass_secret_32_chars_ok' > "$QIU_PREVIEW_PROTECTION_BYPASS_FILE"
chmod 600 "$QIU_PREVIEW_PROTECTION_BYPASS_FILE"
cat > "$QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE" <<JSON
{"schema_version":"qiu.preview-edge.deployment-attestation.v1","project_id":"$QIU_PREVIEW_PROJECT_ID","deployment_id":"$QIU_PREVIEW_DEPLOYMENT_ID","immutable_url":"$QIU_PREVIEW_DEPLOYMENT_ORIGIN","state":"READY","target":null,"git_commit":"$QIU_PREVIEW_EXPECTED_COMMIT","source_commit":"$QIU_PREVIEW_EXPECTED_COMMIT","release_commit":"$QIU_PREVIEW_EXPECTED_COMMIT","attested_at":"2026-08-10T12:00:00Z"}
JSON
chmod 600 "$QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE"

now="$(date '+%s')"
process_started_epoch="$now"
export QIU_FIXTURE_PROCESS_START
QIU_FIXTURE_PROCESS_START="$(LC_ALL=C date -r "$now" '+%a %b %e %T %Y')"
fixture_sha="$(shasum -a 256 /bin/sleep | awk '{print $1}')"

write_candidate() {
  local destination="$1"
  local verified_epoch="$2"
  local generation="$3"
  jq -n \
    --arg generation "$generation" \
    --arg commit "$QIU_PREVIEW_EXPECTED_COMMIT" \
    --arg hostname qiu-r1-fixture.trycloudflare.com \
    --arg sha "$fixture_sha" \
    --argjson pg_pid "${fixture_pids[0]}" \
    --argjson redis_pid "${fixture_pids[1]}" \
    --argjson trading_pid "${fixture_pids[2]}" \
    --argjson rpc_pid "${fixture_pids[3]}" \
    --argjson api_pid "${fixture_pids[4]}" \
    --argjson frontdoor_pid "${fixture_pids[5]}" \
    --argjson started "$process_started_epoch" \
    --argjson verified "$verified_epoch" '
      {
        schema_version:"qiu.preview-edge.generation.v1",
        generation:$generation,
        committed:true,
        source_commit:$commit,
        ready_epoch:$verified,
        verified_epoch:$verified,
        quick_tunnel:{ephemeral:true,expected_hostname:$hostname},
        components:[
          {name:"authority_postgres",pid:$pg_pid,port:5432,started_epoch:$started,binary_sha256:$sha},
          {name:"redis",pid:$redis_pid,port:6389,started_epoch:$started,binary_sha256:$sha},
          {name:"trading",pid:$trading_pid,port:18081,started_epoch:$started,binary_sha256:$sha},
          {name:"rpc",pid:$rpc_pid,port:18083,started_epoch:$started,binary_sha256:$sha},
          {name:"api",pid:$api_pid,port:18080,started_epoch:$started,binary_sha256:$sha},
          {name:"frontdoor",pid:$frontdoor_pid,port:18084,started_epoch:$started,binary_sha256:$sha}
        ]
      }
    ' > "$destination"
  chmod 600 "$destination"
}

candidate="$fixture/candidate.json"
write_candidate "$candidate" "$now" generation-1
"$gate" commit "$candidate" >/dev/null
"$gate" check | grep -Fx 'generation_ready=generation-1' >/dev/null

# The expected release commit is mandatory for every generation operation.
saved_commit="$QIU_PREVIEW_EXPECTED_COMMIT"
QIU_PREVIEW_EXPECTED_COMMIT=''
export QIU_PREVIEW_EXPECTED_COMMIT
if "$gate" check >/dev/null 2>&1; then
  echo 'empty expected commit was accepted' >&2
  exit 1
fi
QIU_PREVIEW_EXPECTED_COMMIT="$saved_commit"
export QIU_PREVIEW_EXPECTED_COMMIT

# A malformed/half-written candidate fails without replacing the last commit.
printf '%s\n' '{"schema_version":"qiu.preview-edge.generation.v1"}' > "$fixture/partial.json"
chmod 600 "$fixture/partial.json"
if "$gate" commit "$fixture/partial.json" >/dev/null 2>&1; then
  echo 'partial generation was accepted' >&2
  exit 1
fi
jq -e '.generation == "generation-1"' "$runtime/run/committed-generation.json" >/dev/null

# Stale readiness and PID/listener drift both fail closed.
write_candidate "$fixture/stale.json" "$((now - 60))" generation-stale
if "$gate" commit "$fixture/stale.json" >/dev/null 2>&1; then
  echo 'stale generation was accepted' >&2
  exit 1
fi
QIU_FIXTURE_LISTENER_DRIFT=18080
export QIU_FIXTURE_LISTENER_DRIFT
if "$gate" check >/dev/null 2>&1; then
  echo 'listener drift was accepted' >&2
  exit 1
fi
QIU_FIXTURE_LISTENER_DRIFT=''
export QIU_FIXTURE_LISTENER_DRIFT

# A dual IPv4/IPv6 loopback listener is private, but any wildcard mixed into
# that set must make the entire committed generation fail closed.
"$gate" check >/dev/null
QIU_FIXTURE_WILDCARD_PORT=5432
export QIU_FIXTURE_WILDCARD_PORT
if "$gate" check >/dev/null 2>&1; then
  echo 'dual loopback plus wildcard listener was accepted' >&2
  exit 1
fi
QIU_FIXTURE_WILDCARD_PORT=''
export QIU_FIXTURE_WILDCARD_PORT

"$gate" probe > "$fixture/probe.out"
grep -Fx 'protected_path=/api/v1/trading/auth/capabilities status=200 cache=no-store' "$fixture/probe.out" >/dev/null
grep -Fx 'protected_path=/api/v1/data-quality/summary status=200 cache=no-store' "$fixture/probe.out" >/dev/null
if grep -F 'fixture_bypass_secret' "$QIU_FIXTURE_CURL_ARGV_LOG" "$fixture/probe.out" >/dev/null; then
  echo 'protection bypass leaked to argv or output' >&2
  exit 1
fi
[ "$(find "$runtime/run" -maxdepth 1 -type f \( -name 'protected-body.*' -o -name 'protected-headers.*' \) | wc -l | tr -d ' ')" = 0 ]
QIU_FIXTURE_DEPLOYMENT_ID=dpl_00000000000000000000
export QIU_FIXTURE_DEPLOYMENT_ID
if "$gate" probe >/dev/null 2>&1; then
  echo 'deployment provenance drift was accepted' >&2
  exit 1
fi
[ "$(find "$runtime/run" -maxdepth 1 -type f \( -name 'protected-body.*' -o -name 'protected-headers.*' \) | wc -l | tr -d ' ')" = 0 ]
QIU_FIXTURE_DEPLOYMENT_ID=dpl_12345678901234567890
export QIU_FIXTURE_DEPLOYMENT_ID

# A deployment attestation may never identify a production target.
jq '.target="production"' "$QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE" > "$fixture/production-attestation.json"
chmod 600 "$fixture/production-attestation.json"
QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE="$fixture/production-attestation.json"
export QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE
if "$gate" probe >/dev/null 2>&1; then
  echo 'production deployment attestation was accepted' >&2
  exit 1
fi
QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE="$runtime/deployment-attestation.json"
export QIU_PREVIEW_DEPLOYMENT_ATTESTATION_FILE

"$gate" drain planned_restart | grep -Fx 'preview_edge_draining=true reason=planned_restart' >/dev/null
[ ! -e "$runtime/run/committed-generation.json" ]
"$gate" status | grep -Fx 'preview_edge_status=draining' >/dev/null
QIU_FIXTURE_DRAIN=true
export QIU_FIXTURE_DRAIN
"$gate" probe-drain > "$fixture/drain.out"
grep -Fx 'protected_path=/api/v1/trading/auth/capabilities status=503 cache=no-store' "$fixture/drain.out" >/dev/null
grep -Fx 'protected_path=/api/v1/data-quality/summary status=503 cache=no-store' "$fixture/drain.out" >/dev/null

QIU_FIXTURE_DRAIN=false
export QIU_FIXTURE_DRAIN
now="$(date '+%s')"
write_candidate "$candidate" "$now" generation-2
"$gate" commit "$candidate" >/dev/null
"$gate" resume | grep -Fx 'preview_edge_draining=false generation=generation-2' >/dev/null
"$gate" check >/dev/null

chmod 644 "$QIU_PREVIEW_PROTECTION_BYPASS_FILE"
if "$gate" probe >/dev/null 2>&1; then
  echo 'unsafe bypass ACL was accepted' >&2
  exit 1
fi
chmod 600 "$QIU_PREVIEW_PROTECTION_BYPASS_FILE"
QIU_PREVIEW_DEPLOYMENT_ORIGIN=https://qiu-market.vercel.app
export QIU_PREVIEW_DEPLOYMENT_ORIGIN
if "$gate" probe >/dev/null 2>&1; then
  echo 'production alias was accepted as immutable Preview' >&2
  exit 1
fi

"$gate" clear >/dev/null
"$gate" status | grep -Fx 'preview_edge_status=not_ready' >/dev/null

bash -n "$gate"
echo 'Preview edge generation, drain, and protected probe fixtures passed.'
