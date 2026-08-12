#!/bin/bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d /tmp/qiu-market-live-cutover.XXXXXX)"
fixture="$(cd "$fixture" && pwd -P)"
runtime="$fixture"
redis_pid=''
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ -n "$redis_pid" ] && kill -0 "$redis_pid" 2>/dev/null; then kill -TERM "$redis_pid" 2>/dev/null || true; wait "$redis_pid" 2>/dev/null || true; fi
  find "$fixture" -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

for command in jq shasum redis-server redis-cli; do command -v "$command" >/dev/null; done
for path in config ops bin run secrets releases fixture-source logs redis; do install -d -m 700 "$runtime/$path"; done
printf '%s\n' 'fixture-only-not-a-live-secret' > "$runtime/secrets/public-proxy-hmac"
chmod 600 "$runtime/secrets/public-proxy-hmac"
port=''
for _ in $(jot 100); do
  candidate_port="$((20000 + RANDOM % 20000))"
  case "$candidate_port" in 6379|6380|6389) continue ;; esac
  if ! lsof -nP -iTCP:"$candidate_port" -sTCP:LISTEN >/dev/null 2>&1; then port="$candidate_port"; break; fi
done
[ -n "$port" ]
redis-server --bind 127.0.0.1 --port "$port" --protected-mode yes --save '' --appendonly no \
  --dir "$runtime/redis" --pidfile "$runtime/run/test-redis.pid" --logfile "$runtime/logs/test-redis.log" &
redis_pid=$!
for _ in $(jot 100); do redis-cli -h 127.0.0.1 -p "$port" ping >/dev/null 2>&1 && break; sleep 0.05; done
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw ping)" = PONG ]

old_commit='1111111111111111111111111111111111111111'
new_commit='2222222222222222222222222222222222222222'
failed_commit='3333333333333333333333333333333333333333'
old_owner='11111111111111111111111111111111'
new_owner='22222222222222222222222222222222'
failed_owner='33333333333333333333333333333333'

make_release() {
  local commit="$1" owner="$2" generation="$3" redis_generation="$4" output="$5"
  local source="$runtime/fixture-source/$commit" config="$runtime/releases/$commit/config"
  local binary="$runtime/bin/market-services.${commit:0:8}" frontdoor="$runtime/bin/market-frontdoor.${commit:0:8}"
  local gate="$runtime/ops/r1/preview-edge-gate.${commit:0:8}" attestation="$config/preview-deployment-attestation.json"
  local deployment="dpl_${commit:0:24}" origin="https://qiu-market-${commit:0:8}.vercel.app"
  install -d -m 700 "$source/migrations" "$config" "$runtime/ops/r1"
  printf '#!/bin/bash\necho market-%s\n' "$commit" > "$binary"; chmod 700 "$binary"
  printf '#!/bin/bash\necho frontdoor-%s\n' "$commit" > "$frontdoor"; chmod 700 "$frontdoor"
  printf '#!/bin/bash\nexit 0\n' > "$gate"; chmod 700 "$gate"
  printf '%s' "$commit" > "$config/source-commit"; chmod 600 "$config/source-commit"
  printf '%s' "$deployment" > "$config/preview-deployment-id"; chmod 600 "$config/preview-deployment-id"
  printf '%s' "$origin" > "$config/allowed-origin"; chmod 600 "$config/allowed-origin"
  jq -n --arg deployment "$deployment" --arg origin "$origin" --arg commit "$commit" '
    {schema_version:"qiu.preview-edge.deployment-attestation.v1",deployment_id:$deployment,
     immutable_url:$origin,state:"READY",target:null,git_commit:$commit,source_commit:$commit,
     release_commit:$commit,attested_at:"2026-08-12T00:00:00Z"}
  ' > "$attestation"; chmod 600 "$attestation"
  jq -n --arg commit "$commit" --arg deployment "$deployment" --arg origin "$origin" \
    --arg source "$source" --arg config "$config" --arg binary "$binary" \
    --arg binary_sha "$(shasum -a 256 "$binary" | awk '{print $1}')" \
    --arg frontdoor "$frontdoor" --arg frontdoor_sha "$(shasum -a 256 "$frontdoor" | awk '{print $1}')" \
    --arg gate "$gate" --arg gate_sha "$(shasum -a 256 "$gate" | awk '{print $1}')" \
    --arg attestation "$attestation" --arg attestation_sha "$(shasum -a 256 "$attestation" | awk '{print $1}')" \
    --arg generation "$generation" --arg owner "$owner" --arg redis_generation "$redis_generation" '
    {schema_version:"qiu.d1.active-release.v2",commit:$commit,data_mode:"live",
     provider_policy:"restricted-no-bypass.v1",contract_schema:"qiu.market-read-contract.v1",
     snapshot_schema:"qiu.market-snapshot.v1",edge_schema:"qiu.market-edge-contract.v1",
     generation_id:$generation,generation_owner_token:$owner,frontdoor_port:18084,
     tunnel_target:"http://127.0.0.1:18084",deployment_id:$deployment,origin:$origin,
     source_path:$source,config_path:$config,binary_path:$binary,binary_sha256:$binary_sha,
     frontdoor_binary_path:$frontdoor,frontdoor_sha256:$frontdoor_sha,
     gate_path:$gate,gate_sha256:$gate_sha,attestation_path:$attestation,
     attestation_sha256:$attestation_sha,redis_generation:$redis_generation,
     redis_owner_token:$owner,redis_pidfile:"'"$runtime"'/run/redis.pid",
     selected_at:"2026-08-12T00:00:00Z"}
  ' > "$output"
  chmod 600 "$output"
}

make_release "$old_commit" "$old_owner" old-generation old-redis "$runtime/old.json"
make_release "$new_commit" "$new_owner" new-generation new-redis "$runtime/new.json"
make_release "$failed_commit" "$failed_owner" failed-generation failed-redis "$runtime/failed.json"
install -m 600 "$runtime/old.json" "$runtime/config/active-release.json"
install -m 600 "$runtime/old.json" "$runtime/releases/$old_commit/release.json"
jq -n --arg commit "$old_commit" --arg owner "$old_owner" '
  {schema_version:"qiu.d1.committed-generation.v2",generation_id:"old-generation",owner_token:$owner,
   commit:$commit,data_mode:"live",frontdoor_port:18084,upstream_port:18080,
   tunnel_target:"http://127.0.0.1:18084",ready:true,verified_at:"2026-08-12T00:00:00Z",
   processes:{stack:999981,redis:999982,api:999983,trading:999984,rpc:999985,frontdoor:999986,
              crawler:999987,dex:999988,worker:999989,tunnel:999990}}
' > "$runtime/run/committed-generation.json"; chmod 600 "$runtime/run/committed-generation.json"
for old_pid in $(jq -r '.processes[]' "$runtime/run/committed-generation.json"); do
  ! kill -0 "$old_pid" 2>/dev/null
done
for pair in live-release-selector.sh:release-selector live-role.sh:live-role live-api-tunnel.sh:live-api-tunnel live-frontdoor.sh:r1/frontdoor live-stack.sh:r1/stack; do
  install -m 700 "$repo_root/ops/macos/${pair%%:*}" "$runtime/ops/${pair##*:}"
done
frontdoor_label='com.qiu-market.d1r1.frontdoor'
stack_label='com.qiu-market.d1r1.stack'
printf '%s\n' "$runtime/ops/r1/frontdoor" > "$runtime/run/test-$frontdoor_label.program"
printf '%s\n' "$runtime/ops/r1/stack" > "$runtime/run/test-$stack_label.program"
for label in com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker; do
  printf '%s\n' "$runtime/ops/live-role" > "$runtime/run/test-$label.program"
done
printf '%s\n' "$runtime/ops/live-api-tunnel" > "$runtime/run/test-com.qiu-market.live.api-tunnel.program"
chmod 600 "$runtime/run"/test-*.program

restart_hook="$runtime/restart-hook"
cat > "$restart_hook" <<'HOOK'
#!/bin/bash
set -euo pipefail
action="$1"; label="$2"; runtime="${QIU_MARKET_LIVE_RUNTIME:?}"
printf '%s %s\n' "$action" "$label" >> "$runtime/run/restart-events"
case "$action" in
  pause)
    find "$runtime/run/test-$label.pid" "$runtime/run/test-$label.sha" -maxdepth 0 -type f -delete 2>/dev/null || true
    [ "$label" != com.qiu-market.live.api-tunnel ] || printf 'paused\n' > "$runtime/run/tunnel-state"
    if [ "${QIU_MARKET_LIVE_FAIL_PAUSE_LABEL:-}" = "$label" ] &&
      [ -f "$runtime/run/fail-pause-armed" ]; then
      exit 75
    fi
    ;;
  start)
    if [ -n "${QIU_MARKET_LIVE_FAIL_RESTORE_COMMIT:-}" ] &&
      [ "$(jq -r '.commit' "$runtime/config/active-release.json")" = "$QIU_MARKET_LIVE_FAIL_RESTORE_COMMIT" ]; then
      exit 75
    fi
    case "$label" in
      com.qiu-market.d1r1.frontdoor) fake_pid=88001; sha="$(jq -r '.frontdoor_sha256' "$runtime/config/active-release.json")" ;;
      com.qiu-market.d1r1.stack) fake_pid=88002; sha="$(jq -r '.binary_sha256' "$runtime/config/active-release.json")" ;;
      com.qiu-market.live.crawler) fake_pid=88003; sha="$(jq -r '.binary_sha256' "$runtime/config/active-release.json")" ;;
      com.qiu-market.live.dex) fake_pid=88004; sha="$(jq -r '.binary_sha256' "$runtime/config/active-release.json")" ;;
      com.qiu-market.live.worker) fake_pid=88005; sha="$(jq -r '.binary_sha256' "$runtime/config/active-release.json")" ;;
      com.qiu-market.live.api-tunnel)
        fake_pid=88006; sha="$(jq -r '.binary_sha256' "$runtime/config/active-release.json")"
        QIU_MARKET_LIVE_CUTOVER_TEST_MODE=true "$runtime/ops/live-api-tunnel" >/dev/null
        printf 'resumed\n' > "$runtime/run/tunnel-state"
        [ -z "${QIU_MARKET_LIVE_FAIL_PAUSE_LABEL:-}" ] || printf 'armed\n' > "$runtime/run/fail-pause-armed"
        ;;
      *) exit 64 ;;
    esac
    printf '%s\n' "$sha" > "$runtime/run/test-$label.sha"
    printf '%s\n' "$fake_pid" > "$runtime/run/test-$label.pid"
    chmod 600 "$runtime/run/test-$label.sha" "$runtime/run/test-$label.pid"
    ;;
  *) exit 64 ;;
esac
HOOK
chmod 700 "$restart_hook"

probe_hook="$runtime/probe-hook"
cat > "$probe_hook" <<'HOOK'
#!/bin/bash
set -euo pipefail
require_edge="$1"; expected_status="$2"; manifest="$3"; port="$4"
runtime="${QIU_MARKET_LIVE_RUNTIME:?}"
generation="$runtime/run/committed-generation.json"
[ "$(jq -r '.commit' "$manifest")" = "$(jq -r '.commit' "$runtime/config/active-release.json")" ]
ready="$(jq -r '.ready' "$generation")"
has_tunnel="$(jq '(.processes // {}) | has("tunnel")' "$generation")"
case "$require_edge:$expected_status:$port:$ready:$has_tunnel" in
  false:200:18080:false:false|false:200:18080:true:true|true:503:18084:false:false|true:200:18084:true:false|true:200:18084:true:true) ;;
  *) echo "invalid probe sequence edge=$require_edge status=$expected_status port=$port ready=$ready tunnel=$has_tunnel" >&2; exit 1 ;;
esac
printf '%s %s %s %s %s\n' "$require_edge" "$expected_status" "$port" "$ready" "$has_tunnel" >> "$runtime/run/probe-events"
HOOK
chmod 700 "$probe_hook"

health_hook="$runtime/health-hook"
cat > "$health_hook" <<'HOOK'
#!/bin/bash
set -euo pipefail
runtime="${QIU_MARKET_LIVE_RUNTIME:?}"
commit="$(jq -r '.commit' "$runtime/config/active-release.json")"
count_file="$runtime/run/health-polls-$commit"
count=0
[ ! -f "$count_file" ] || count="$(<"$count_file")"
count=$((count + 1))
printf '%s\n' "$count" > "$count_file"
status=503
if [ "${QIU_MARKET_LIVE_FAIL_HEALTH_COMMIT:-}" != "$commit" ] &&
  [ "$count" -gt "${QIU_MARKET_LIVE_HEALTH_DELAY_POLLS:-3}" ]; then
  status=200
fi
printf 'health %s %s\n' "$commit" "$status" >> "$runtime/run/restart-events"
printf '%s\n' "$status"
HOOK
chmod 700 "$health_hook"

export QIU_MARKET_LIVE_RUNTIME="$runtime"
export QIU_MARKET_LIVE_CUTOVER_TEST_MODE=true
export QIU_MARKET_LIVE_RESTART_HOOK="$restart_hook"
export QIU_MARKET_LIVE_PROBE_HOOK="$probe_hook"
export QIU_MARKET_LIVE_HEALTH_HOOK="$health_hook"
export QIU_MARKET_LIVE_REDIS_PORT="$port"
cutover="$repo_root/ops/macos/live-cutover.sh"

make_rollback_backup() {
  local name="$1" destination relative
  destination="$runtime/run/live-cutover/backup-$name"
  install -d -m 700 "$destination"
  for relative in config/active-release.json run/committed-generation.json ops/release-selector \
    ops/live-role ops/live-api-tunnel ops/r1/frontdoor ops/r1/stack; do
    install -d -m 700 "$destination/$(dirname "$relative")"
    install -m "$( [ -x "$runtime/$relative" ] && echo 700 || echo 600 )" \
      "$runtime/$relative" "$destination/$relative"
  done
  printf '%s\n' "$destination"
}

rollback_authority="$(make_rollback_backup old-known-good)"
"$cutover" seal-rollback "$rollback_authority" | grep -Fx 'live_cutover_rollback_seal=passed mutated=seal-only' >/dev/null
"$cutover" preflight "$runtime/new.json" "$rollback_authority" | grep -Fx 'live_cutover_preflight=passed mutated=false' >/dev/null

prepared_output="$("$cutover" prepare-rollback --execute)"
prepared_authority="${prepared_output#live_cutover_rollback_authority=}"
prepared_authority="${prepared_authority%% commit=*}"
[ -f "$prepared_authority/rollback-authority.json" ]
"$cutover" preflight "$runtime/new.json" "$prepared_authority" >/dev/null

authority_count_before="$(find "$runtime/run/live-cutover" -maxdepth 2 -name rollback-authority.json | wc -l | tr -d ' ')"
export QIU_MARKET_LIVE_FAIL_HEALTH_COMMIT="$old_commit"
export QIU_MARKET_LIVE_TEST_HEALTH_TIMEOUT_SECONDS=1
if "$cutover" prepare-rollback --execute >/dev/null 2>&1; then echo 'unhealthy current generation produced rollback authority' >&2; exit 1; fi
unset QIU_MARKET_LIVE_FAIL_HEALTH_COMMIT QIU_MARKET_LIVE_TEST_HEALTH_TIMEOUT_SECONDS
[ "$(find "$runtime/run/live-cutover" -maxdepth 2 -name rollback-authority.json | wc -l | tr -d ' ')" = "$authority_count_before" ]

drifted_rollback="$(make_rollback_backup drifted-wrapper)"
"$cutover" seal-rollback "$drifted_rollback" >/dev/null
printf '\n# syntax-valid drift\n' >> "$drifted_rollback/ops/live-role"
if "$cutover" preflight "$runtime/new.json" "$drifted_rollback" >/dev/null 2>&1; then echo 'digest-drifted rollback wrapper was accepted' >&2; exit 1; fi

restart_event_count() {
  [ -f "$runtime/run/restart-events" ] && wc -l < "$runtime/run/restart-events" || echo 0
}
restart_before_invalid_rollback="$(restart_event_count)"
invalid_rollback="$(make_rollback_backup invalid)"
"$cutover" seal-rollback "$invalid_rollback" >/dev/null
chmod 644 "$invalid_rollback/config/active-release.json"
if "$cutover" preflight "$runtime/new.json" "$invalid_rollback" >/dev/null 2>&1; then echo 'unsafe rollback authority was accepted' >&2; exit 1; fi
if "$cutover" cutover "$runtime/new.json" "$invalid_rollback" --execute >/dev/null 2>&1; then echo 'cutover accepted unsafe rollback authority' >&2; exit 1; fi
[ "$(restart_event_count)" = "$restart_before_invalid_rollback" ]

mv "$runtime/run/test-$frontdoor_label.program" "$runtime/run/test-$frontdoor_label.program.saved"
if "$cutover" preflight "$runtime/new.json" "$rollback_authority" >/dev/null 2>&1; then echo 'missing live frontdoor label was accepted' >&2; exit 1; fi
mv "$runtime/run/test-$frontdoor_label.program.saved" "$runtime/run/test-$frontdoor_label.program"
printf '%s\n' "$runtime/ops/r1/wrong-stack" > "$runtime/run/test-$stack_label.program"
if "$cutover" preflight "$runtime/new.json" "$rollback_authority" >/dev/null 2>&1; then echo 'wrong live stack Program path was accepted' >&2; exit 1; fi
printf '%s\n' "$runtime/ops/r1/stack" > "$runtime/run/test-$stack_label.program"
printf '%s\n' "$runtime/ops/wrong-live-role" > "$runtime/run/test-com.qiu-market.live.dex.program"
if "$cutover" preflight "$runtime/new.json" "$rollback_authority" >/dev/null 2>&1; then echo 'wrong live role Program path was accepted' >&2; exit 1; fi
printf '%s\n' "$runtime/ops/live-role" > "$runtime/run/test-com.qiu-market.live.dex.program"
chmod 644 "$runtime/secrets/public-proxy-hmac"
if "$cutover" preflight "$runtime/new.json" "$rollback_authority" >/dev/null 2>&1; then echo 'world-readable probe secret was accepted' >&2; exit 1; fi
chmod 600 "$runtime/secrets/public-proxy-hmac"

jq '.tunnel_target="http://127.0.0.1:18080"' "$runtime/new.json" > "$runtime/direct.json"; chmod 600 "$runtime/direct.json"
if "$cutover" preflight "$runtime/direct.json" "$rollback_authority" >/dev/null 2>&1; then echo 'direct API tunnel target was accepted' >&2; exit 1; fi

mkdir -p "$runtime/run/live-cutover/lock"; printf '999\n' > "$runtime/run/live-cutover/lock/owner"; chmod 600 "$runtime/run/live-cutover/lock/owner"
if "$cutover" cutover "$runtime/new.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'concurrent cutover lock was ignored' >&2; exit 1; fi
find "$runtime/run/live-cutover/lock/owner" -maxdepth 0 -type f -delete; rmdir "$runtime/run/live-cutover/lock"

redis-cli -h 127.0.0.1 -p "$port" SET qiu:runtime-generation:old-redis:owner "$old_owner" >/dev/null
redis-cli -h 127.0.0.1 -p "$port" SET qiu:runtime-generation:old-redis:state committed:old-generation >/dev/null
redis-cli -h 127.0.0.1 -p "$port" SET qiu:runtime-generation:new-redis:sentinel new-only >/dev/null
cutover_first_line="$(( $(wc -l < "$runtime/run/restart-events") + 1 ))"
probe_first_line="$(( $(wc -l < "$runtime/run/probe-events") + 1 ))"
"$cutover" cutover "$runtime/new.json" "$rollback_authority" --execute | grep -F "live_cutover=ready commit=$new_commit tunnel_target=http://127.0.0.1:18084 production_promoted=false" >/dev/null
[ "$(jq -r '.commit' "$runtime/config/active-release.json")" = "$new_commit" ]
[ "$(stat -f '%Lp' "$runtime/releases/$new_commit/release.json")" = 600 ]
[ "$(jq -r '.commit' "$runtime/releases/$new_commit/release.json")" = "$new_commit" ]
[ "$(<"$runtime/run/tunnel-state")" = resumed ]
[ "$(jq -r '.processes.frontdoor' "$runtime/run/committed-generation.json")" = 88001 ]
[ "$(jq -r '.processes.stack' "$runtime/run/committed-generation.json")" = 88002 ]
[ "$(sed -n "$((cutover_first_line + 1))p" "$runtime/run/restart-events")" = "start $frontdoor_label" ]
[ "$(sed -n "$((cutover_first_line + 2))p" "$runtime/run/restart-events")" = "start $stack_label" ]
[ "$(awk -v commit="$new_commit" '$1=="health" && $2==commit {print $3}' "$runtime/run/restart-events" | sed -n '1,4p')" = $'503\n503\n503\n200' ]
health_ready_line="$(grep -n -m1 "health $new_commit 200" "$runtime/run/restart-events" | cut -d: -f1)"
crawler_start_line="$(grep -n -m1 'start com.qiu-market.live.crawler' "$runtime/run/restart-events" | cut -d: -f1)"
[ "$health_ready_line" -lt "$crawler_start_line" ]
[ "$(jq -r '.ready' "$runtime/run/committed-generation.json")" = true ]
[ "$(jq -r '.processes | has("tunnel")' "$runtime/run/committed-generation.json")" = true ]
[ "$(sed -n "${probe_first_line}p" "$runtime/run/probe-events")" = 'false 200 18080 false false' ]
[ "$(sed -n "$((probe_first_line + 1))p" "$runtime/run/probe-events")" = 'true 503 18084 false false' ]
[ "$(sed -n "$((probe_first_line + 2))p" "$runtime/run/probe-events")" = 'true 200 18084 true false' ]
[ "$(sed -n "$((probe_first_line + 3))p" "$runtime/run/probe-events")" = 'true 200 18084 true true' ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw EXISTS qiu:runtime-generation:old-redis:owner)" = 0 ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:owner)" = "$new_owner" ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:state)" = committed:new-generation ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:sentinel)" = new-only ]
for role in crawler dex worker; do
  "$runtime/ops/live-role" "$role" | grep -F "role=$role commit=$new_commit" >/dev/null
done
"$runtime/ops/live-api-tunnel" | grep -F 'tunnel_target=http://127.0.0.1:18084 generation=new-generation' >/dev/null
"$runtime/ops/r1/frontdoor" | grep -F 'listen=127.0.0.1:18084 upstream=http://127.0.0.1:18080 generation=new-generation' >/dev/null
"$runtime/ops/r1/stack" | grep -F 'business_stack=true' >/dev/null

rollback_authority="$(make_rollback_backup new-known-good)"
"$cutover" seal-rollback "$rollback_authority" >/dev/null

pause_before_owner_drift="$(restart_event_count)"
redis-cli -h 127.0.0.1 -p "$port" SET qiu:runtime-generation:new-redis:owner "$failed_owner" >/dev/null
if "$cutover" cutover "$runtime/failed.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'rollback Redis owner drift was accepted' >&2; exit 1; fi
[ "$(restart_event_count)" = "$pause_before_owner_drift" ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:owner)" = "$failed_owner" ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:state)" = committed:new-generation ]
redis-cli -h 127.0.0.1 -p "$port" SET qiu:runtime-generation:new-redis:owner "$new_owner" >/dev/null

printf '22222\n' > "$runtime/run/redis.pid"; chmod 600 "$runtime/run/redis.pid"
"$cutover" pidfile-cleanup "$runtime/run/redis.pid" 11111 "$port"
[ "$(<"$runtime/run/redis.pid")" = 22222 ]

export QIU_MARKET_LIVE_FAIL_HEALTH_COMMIT="$failed_commit"
export QIU_MARKET_LIVE_TEST_HEALTH_TIMEOUT_SECONDS=1
if "$cutover" cutover "$runtime/failed.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'business API health timeout was accepted' >&2; exit 1; fi
unset QIU_MARKET_LIVE_FAIL_HEALTH_COMMIT QIU_MARKET_LIVE_TEST_HEALTH_TIMEOUT_SECONDS
[ "$(jq -r '.commit' "$runtime/config/active-release.json")" = "$new_commit" ]
[ "$(jq -r '.generation_id' "$runtime/run/committed-generation.json")" = new-generation ]
[ "$(<"$runtime/run/tunnel-state")" = resumed ]
[ "$(jq -r '.phase' "$runtime/run/live-cutover/current.json")" = rolled-back ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw EXISTS qiu:runtime-generation:failed-redis:owner)" = 0 ]

export QIU_MARKET_LIVE_CUTOVER_FAIL_AT=after_restart
if "$cutover" cutover "$runtime/failed.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'injected cutover failure did not fail' >&2; exit 1; fi
unset QIU_MARKET_LIVE_CUTOVER_FAIL_AT
[ "$(jq -r '.commit' "$runtime/config/active-release.json")" = "$new_commit" ]
[ "$(jq -r '.generation_id' "$runtime/run/committed-generation.json")" = new-generation ]
[ "$(<"$runtime/run/tunnel-state")" = resumed ]
[ "$(jq -r '.phase' "$runtime/run/live-cutover/current.json")" = rolled-back ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:new-redis:state)" = committed:new-generation ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw EXISTS qiu:runtime-generation:failed-redis:owner)" = 0 ]

export QIU_MARKET_LIVE_CUTOVER_FAIL_AT=after_restart
export QIU_MARKET_LIVE_FAIL_RESTORE_COMMIT="$new_commit"
if "$cutover" cutover "$runtime/failed.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'rollback failure injection did not fail' >&2; exit 1; fi
unset QIU_MARKET_LIVE_CUTOVER_FAIL_AT QIU_MARKET_LIVE_FAIL_RESTORE_COMMIT
[ "$(jq -r '.phase' "$runtime/run/live-cutover/current.json")" = rollback-failed ]
[ "$(jq -r '.commit' "$runtime/config/active-release.json")" = "$new_commit" ]
[ "$(<"$runtime/run/tunnel-state")" = paused ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:failed-redis:owner)" = "$failed_owner" ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:failed-redis:state)" = committed:failed-generation ]
redis-cli -h 127.0.0.1 -p "$port" DEL qiu:runtime-generation:failed-redis:owner \
  qiu:runtime-generation:failed-redis:state qiu:runtime-generation:failed-redis:lock >/dev/null

pause_test_first_line="$(( $(wc -l < "$runtime/run/restart-events") + 1 ))"
export QIU_MARKET_LIVE_CUTOVER_FAIL_AT=after_restart
export QIU_MARKET_LIVE_FAIL_PAUSE_LABEL=com.qiu-market.live.api-tunnel
if "$cutover" cutover "$runtime/failed.json" "$rollback_authority" --execute >/dev/null 2>&1; then echo 'pause failure injection did not fail' >&2; exit 1; fi
unset QIU_MARKET_LIVE_CUTOVER_FAIL_AT QIU_MARKET_LIVE_FAIL_PAUSE_LABEL
pause_failure_events="$(tail -n "+$pause_test_first_line" "$runtime/run/restart-events")"
for label in com.qiu-market.live.api-tunnel com.qiu-market.live.crawler com.qiu-market.live.dex \
  com.qiu-market.live.worker "$stack_label" "$frontdoor_label"; do
  grep -F "pause $label" <<<"$pause_failure_events" >/dev/null
done
tunnel_pause_line="$(grep -n -m1 'pause com.qiu-market.live.api-tunnel' <<<"$pause_failure_events" | cut -d: -f1)"
frontdoor_pause_line="$(grep -n -m1 "pause $frontdoor_label" <<<"$pause_failure_events" | cut -d: -f1)"
[ "$frontdoor_pause_line" -gt "$tunnel_pause_line" ]
[ "$(jq -r '.phase' "$runtime/run/live-cutover/current.json")" = rollback-failed ]
[ "$(<"$runtime/run/tunnel-state")" = paused ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:failed-redis:owner)" = "$failed_owner" ]
[ "$(redis-cli -h 127.0.0.1 -p "$port" --raw GET qiu:runtime-generation:failed-redis:state)" = committed:failed-generation ]

echo 'Live selector, pure frontdoor target, Redis ownership, pidfile race, concurrent lock, and rollback fixtures passed.'
