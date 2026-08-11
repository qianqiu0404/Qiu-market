#!/bin/bash
set -euo pipefail
umask 077

action="${1:-status}"
candidate="${2:-}"
execute_flag="${3:-}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
state_dir="$runtime/run/live-cutover"
state_file="$state_dir/current.json"
lock_dir="$state_dir/lock"
domain="gui/$(id -u)"
frontdoor_label='com.qiu-market.d1r1.frontdoor'
stack_label='com.qiu-market.d1r1.stack'
owns_lock=false

if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  case "$runtime" in /tmp/qiu-market-live-cutover.*) ;; *) echo 'test mode requires an isolated /tmp runtime' >&2; exit 64 ;; esac
fi

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ "$owns_lock" = true ] && [ -d "$lock_dir" ] && [ -f "$lock_dir/owner" ] && [ "$(<"$lock_dir/owner")" = "$$" ]; then
    find "$lock_dir/owner" -maxdepth 0 -type f -delete 2>/dev/null || true
    rmdir "$lock_dir" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

acquire_lock() {
  install -d -m 700 "$state_dir"
  mkdir "$lock_dir" 2>/dev/null || { echo 'another live cutover owns the lock' >&2; return 1; }
  owns_lock=true
  printf '%s\n' "$$" > "$lock_dir/owner"; chmod 600 "$lock_dir/owner"
}

selector_preflight() {
  local manifest="$1"
  QIU_MARKET_LIVE_RUNTIME="$runtime" \
    QIU_MARKET_LIVE_CUTOVER_TEST_MODE="${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" \
    bash -c 'source "$1"; validate_release_selector "$2"' _ \
    "$repo_root/ops/macos/live-release-selector.sh" "$manifest"
  verify_launch_contract
}

label_program_path() {
  local label="$1"
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    [ -f "$runtime/run/test-$label.program" ] || return 1
    printf '%s\n' "$(<"$runtime/run/test-$label.program")"
  else
    launchctl print "$domain/$label" 2>/dev/null | awk -F' = ' '/^[[:space:]]*program = /{print $2;exit}'
  fi
}

verify_launch_contract() {
  local actual label expected
  actual="$(label_program_path "$frontdoor_label")" || { echo 'live frontdoor LaunchAgent is missing' >&2; return 1; }
  [ "$actual" = "$runtime/ops/r1/frontdoor" ] || { echo 'live frontdoor LaunchAgent Program path mismatch' >&2; return 1; }
  actual="$(label_program_path "$stack_label")" || { echo 'live stack LaunchAgent is missing' >&2; return 1; }
  [ "$actual" = "$runtime/ops/r1/stack" ] || { echo 'live stack LaunchAgent Program path mismatch' >&2; return 1; }
  for label in com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker; do
    actual="$(label_program_path "$label")" || { echo "live role LaunchAgent is missing: $label" >&2; return 1; }
    expected="$runtime/ops/live-role"
    [ "$actual" = "$expected" ] || { echo "live role LaunchAgent Program path mismatch: $label" >&2; return 1; }
  done
  label='com.qiu-market.live.api-tunnel'
  actual="$(label_program_path "$label")" || { echo 'live API tunnel LaunchAgent is missing' >&2; return 1; }
  [ "$actual" = "$runtime/ops/live-api-tunnel" ] || { echo 'live API tunnel LaunchAgent Program path mismatch' >&2; return 1; }
}

listener_pid() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true
}

owner_checked_remove_pidfile() {
  local pidfile="$1" expected_old_pid="$2" port="$3" current listener
  [ -f "$pidfile" ] && [ ! -L "$pidfile" ] || return 0
  current="$(<"$pidfile")"
  [ "$current" = "$expected_old_pid" ] || return 0
  listener="$(listener_pid "$port")"
  if [ -n "$listener" ] && [ "$listener" != "$expected_old_pid" ]; then
    return 0
  fi
  kill -0 "$expected_old_pid" 2>/dev/null && return 1
  find "$pidfile" -maxdepth 0 -type f -delete
}

start_label() {
  local label="$1"
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    "${QIU_MARKET_LIVE_RESTART_HOOK:?}" start "$label"
  else
    launchctl enable "$domain/$label" || return 1
    launchctl kickstart -k "$domain/$label"
  fi
}

pause_label() {
  local label="$1"
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    "${QIU_MARKET_LIVE_RESTART_HOOK:?}" pause "$label"
  else
    launchctl disable "$domain/$label" || return 1
    launchctl kill SIGTERM "$domain/$label" >/dev/null 2>&1 || true
  fi
}

verify_label_binary() {
  local label="$1" expected_sha="$2" pid executable actual
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    [ "$(<"$runtime/run/test-$label.sha")" = "$expected_sha" ]
    [ -s "$runtime/run/test-$label.pid" ]
    return
  fi
  pid="$(launchctl print "$domain/$label" | awk '/pid = /{print $3;exit}')"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  executable="$(lsof -a -p "$pid" -d txt -Fn 2>/dev/null | awk '/^n/{sub(/^n/,"");print;exit}')"
  [ -n "$executable" ] || return 1
  actual="$(shasum -a 256 "$executable" | awk '{print $1}')"
  [ "$actual" = "$expected_sha" ]
}

write_generation() {
  local manifest="$1" destination="$2" ready="$3" processes
  local temporary="$destination.$$.tmp"
  if [ "$#" -ge 4 ]; then processes="$4"; else processes='{}'; fi
  jq -n --arg schema 'qiu.d1.committed-generation.v2' \
    --arg generation "$(jq -r '.generation_id' "$manifest")" \
    --arg owner "$(jq -r '.generation_owner_token' "$manifest")" \
    --arg commit "$(jq -r '.commit' "$manifest")" \
    --arg target 'http://127.0.0.1:18084' \
    --arg verified "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --argjson ready "$ready" --argjson processes "$processes" '
      {schema_version:$schema,generation_id:$generation,owner_token:$owner,commit:$commit,
       data_mode:"live",frontdoor_port:18084,upstream_port:18080,tunnel_target:$target,
       ready:$ready,verified_at:$verified,processes:$processes}
    ' > "$temporary"
  chmod 600 "$temporary"; mv "$temporary" "$destination"
}

install_runtime_consumers() {
  install -m 700 "$repo_root/ops/macos/live-release-selector.sh" "$runtime/ops/release-selector.next"
  install -m 700 "$repo_root/ops/macos/live-role.sh" "$runtime/ops/live-role.next"
  install -m 700 "$repo_root/ops/macos/live-api-tunnel.sh" "$runtime/ops/live-api-tunnel.next"
  install -m 700 "$repo_root/ops/macos/live-frontdoor.sh" "$runtime/ops/r1/frontdoor.next"
  install -m 700 "$repo_root/ops/macos/live-stack.sh" "$runtime/ops/r1/stack.next"
  mv "$runtime/ops/release-selector.next" "$runtime/ops/release-selector"
  mv "$runtime/ops/live-role.next" "$runtime/ops/live-role"
  mv "$runtime/ops/live-api-tunnel.next" "$runtime/ops/live-api-tunnel"
  mv "$runtime/ops/r1/frontdoor.next" "$runtime/ops/r1/frontdoor"
  mv "$runtime/ops/r1/stack.next" "$runtime/ops/r1/stack"
}

install_candidate_release_manifest() {
  local manifest="$1" release_root destination
  release_root="$(dirname "$(jq -r '.config_path' "$manifest")")"
  destination="$release_root/release.json"
  [ "$release_root" = "$runtime/releases/$(jq -r '.commit' "$manifest")" ] || return 1
  install -m 600 "$manifest" "$destination.next"
  mv "$destination.next" "$destination"
}

backup_runtime() {
  local backup="$1"
  install -d -m 700 "$backup"
  for relative in config/active-release.json run/committed-generation.json ops/release-selector ops/live-role ops/live-api-tunnel ops/r1/frontdoor ops/r1/stack; do
    [ -e "$runtime/$relative" ] || continue
    install -d -m 700 "$backup/$(dirname "$relative")"
    install -m "$( [ -x "$runtime/$relative" ] && echo 700 || echo 600 )" "$runtime/$relative" "$backup/$relative"
  done
}

restore_runtime() {
  local backup="$1"
  for relative in config/active-release.json run/committed-generation.json ops/release-selector ops/live-role ops/live-api-tunnel ops/r1/frontdoor ops/r1/stack; do
    [ -e "$backup/$relative" ] || continue
    install -m "$( [ -x "$backup/$relative" ] && echo 700 || echo 600 )" "$backup/$relative" "$runtime/$relative.next" || return 1
    mv "$runtime/$relative.next" "$runtime/$relative" || return 1
  done
}

verify_old_processes_stopped() {
  local generation="$1" pid
  [ -f "$generation" ] || return 0
  while IFS= read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    ! kill -0 "$pid" 2>/dev/null || return 1
  done < <(jq -r '.processes // {} | to_entries[] | .value' "$generation")
}

label_pid() {
  local label="$1"
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    printf '%s\n' "$(<"$runtime/run/test-$label.pid")"
  else
    launchctl print "$domain/$label" | awk '/pid = /{print $3;exit}'
  fi
}

wait_label_binary() {
  local label="$1" expected_sha="$2"
  for _ in $(jot 120); do
    verify_label_binary "$label" "$expected_sha" 2>/dev/null && return 0
    sleep 0.25
  done
  return 1
}

wait_label_running() {
  local label="$1" pid
  for _ in $(jot 120); do
    pid="$(label_pid "$label" 2>/dev/null || true)"
    if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
      if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ] || kill -0 "$pid" 2>/dev/null; then
        return 0
      fi
    fi
    sleep 0.25
  done
  return 1
}

wait_label_stopped() {
  local label="$1" pid
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    [ ! -e "$runtime/run/test-$label.pid" ]
    return
  fi
  for _ in $(jot 120); do
    pid="$(label_pid "$label" 2>/dev/null || true)"
    if ! [[ "$pid" =~ ^[1-9][0-9]*$ ]] || ! kill -0 "$pid" 2>/dev/null; then return 0; fi
    sleep 0.25
  done
  return 1
}

wait_frontdoor_binary() {
  local expected_sha="$1" pid executable actual
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    wait_label_binary "$frontdoor_label" "$expected_sha"
    return
  fi
  for _ in $(jot 120); do
    pid="$(listener_pid 18084)"
    if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
      executable="$(lsof -a -p "$pid" -d txt -Fn 2>/dev/null | awk '/^n/{sub(/^n/,"");print;exit}')"
      if [ -n "$executable" ]; then
        actual="$(shasum -a 256 "$executable" | awk '{print $1}')"
        [ "$actual" = "$expected_sha" ] && return 0
      fi
    fi
    sleep 0.25
  done
  return 1
}

capture_generation_processes() {
  local include_tunnel="$1" frontdoor stack crawler dex worker tunnel=0 processes
  frontdoor="$(label_pid "$frontdoor_label")"
  stack="$(label_pid "$stack_label")"
  crawler="$(label_pid 'com.qiu-market.live.crawler')"
  dex="$(label_pid 'com.qiu-market.live.dex')"
  worker="$(label_pid 'com.qiu-market.live.worker')"
  if [ "$include_tunnel" = true ]; then tunnel="$(label_pid 'com.qiu-market.live.api-tunnel')"; fi
  for pid in "$frontdoor" "$stack" "$crawler" "$dex" "$worker"; do [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1; done
  if [ "$include_tunnel" = true ]; then [[ "$tunnel" =~ ^[1-9][0-9]*$ ]] || return 1; fi
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    if [ "$include_tunnel" = true ]; then
      jq -n --argjson frontdoor "$frontdoor" --argjson stack "$stack" --argjson crawler "$crawler" --argjson dex "$dex" --argjson worker "$worker" --argjson tunnel "$tunnel" '
        {stack:$stack,redis:$stack,api:$stack,trading:$stack,rpc:$stack,frontdoor:$frontdoor,
         crawler:$crawler,dex:$dex,worker:$worker,tunnel:$tunnel}'
    else
      jq -n --argjson frontdoor "$frontdoor" --argjson stack "$stack" --argjson crawler "$crawler" --argjson dex "$dex" --argjson worker "$worker" '
        {stack:$stack,redis:$stack,api:$stack,trading:$stack,rpc:$stack,frontdoor:$frontdoor,
         crawler:$crawler,dex:$dex,worker:$worker}'
    fi
    return
  fi
  processes="$(jq -n --argjson stack "$stack" --argjson redis "$(<"$runtime/run/redis.pid")" \
    --argjson api "$(<"$runtime/run/api.pid")" --argjson trading "$(<"$runtime/run/trading.pid")" \
    --argjson rpc "$(<"$runtime/run/rpc.pid")" --argjson frontdoor "$frontdoor" \
    --argjson crawler "$crawler" --argjson dex "$dex" --argjson worker "$worker" '
    {stack:$stack,redis:$redis,api:$api,trading:$trading,rpc:$rpc,frontdoor:$frontdoor,
     crawler:$crawler,dex:$dex,worker:$worker}')"
  if [ "$include_tunnel" = true ]; then
    jq --argjson tunnel "$tunnel" '. + {tunnel:$tunnel}' <<<"$processes"
  else
    printf '%s\n' "$processes"
  fi
}

probe_market_contract() {
  local manifest="$1" port="$2" require_edge="$3" expected_status="$4" binary
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
    "${QIU_MARKET_LIVE_PROBE_HOOK:?}" "$require_edge" "$expected_status" "$manifest" "$port"
    return
  fi
  local secret_file="$runtime/secrets/public-proxy-hmac"
  release_private_file "$secret_file" || return 1
  binary="$(jq -r '.binary_path' "$manifest")"
  if [ "$require_edge" = true ]; then
    "$binary" contract-probe \
      --endpoint "http://127.0.0.1:$port/api/v2/get_market_overview" \
      --secret-file "$secret_file" --expected-release "$(jq -r '.commit' "$manifest")" \
      --expected-status "$expected_status" --require-edge
  else
    "$binary" contract-probe \
      --endpoint "http://127.0.0.1:$port/api/v2/get_market_overview" \
      --secret-file "$secret_file" --expected-release "$(jq -r '.commit' "$manifest")" \
      --expected-status "$expected_status"
  fi
}

redis_command() {
  local port="${QIU_MARKET_LIVE_REDIS_PORT:-6389}"
  local password_file="$runtime/secrets/redis-password"
  if [ -s "$password_file" ]; then
    REDISCLI_AUTH="$(<"$password_file")" redis-cli -h 127.0.0.1 -p "$port" --raw "$@"
  else
    redis-cli -h 127.0.0.1 -p "$port" --raw "$@"
  fi
}

claim_redis_generation() {
  local manifest="$1" generation owner result
  generation="$(jq -r '.redis_generation' "$manifest")"; owner="$(jq -r '.redis_owner_token' "$manifest")"
  result="$(redis_command EVAL '
    local current = redis.call("GET", KEYS[1])
    if current and current ~= ARGV[1] then return -1 end
    if not current then redis.call("SET", KEYS[1], ARGV[1]) end
    redis.call("SET", KEYS[2], ARGV[2])
    return 1
  ' 2 "qiu:runtime-generation:$generation:owner" "qiu:runtime-generation:$generation:state" \
    "$owner" "committed:$(jq -r '.generation_id' "$manifest")")"
  [ "$result" = 1 ] || { echo 'new Redis generation ownership claim refused' >&2; return 1; }
}

release_uncommitted_redis_generation() {
  local manifest="$1" generation owner result
  [ "$(jq -r '.schema_version // empty' "$manifest" 2>/dev/null || true)" = 'qiu.d1.active-release.v2' ] || return 0
  generation="$(jq -r '.redis_generation' "$manifest")"; owner="$(jq -r '.redis_owner_token' "$manifest")"
  result="$(redis_command EVAL '
    local current = redis.call("GET", KEYS[1])
    if not current then return 0 end
    if current ~= ARGV[1] then return -1 end
    redis.call("DEL", KEYS[2], KEYS[3], KEYS[1])
    return 1
  ' 3 "qiu:runtime-generation:$generation:owner" "qiu:runtime-generation:$generation:state" \
    "qiu:runtime-generation:$generation:lock" "$owner")"
  [ "$result" != -1 ] || { echo 'uncommitted Redis generation ownership mismatch; cleanup refused' >&2; return 1; }
}

cleanup_old_redis_generation() {
  local manifest="$1" committed_generation="$2" generation owner owner_key state_key lock_key result old_pid listener
  [ -f "$manifest" ] || return 0
  [ "$(jq -r '.schema_version // empty' "$manifest")" = 'qiu.d1.active-release.v2' ] || return 0
  [ -f "$committed_generation" ] || { echo 'old committed generation evidence is missing' >&2; return 1; }
  verify_old_processes_stopped "$committed_generation" || { echo 'old generation still has running processes' >&2; return 1; }
  old_pid="$(jq -r '.processes.redis // empty' "$committed_generation")"
  [[ "$old_pid" =~ ^[1-9][0-9]*$ ]] || { echo 'old Redis PID ownership evidence is missing' >&2; return 1; }
  listener="$(listener_pid "${QIU_MARKET_LIVE_REDIS_PORT:-6389}")"
  [ "$listener" != "$old_pid" ] || { echo 'old Redis generation still owns the listener' >&2; return 1; }
  generation="$(jq -r '.redis_generation' "$manifest")"; owner="$(jq -r '.redis_owner_token' "$manifest")"
  owner_key="qiu:runtime-generation:$generation:owner"
  state_key="qiu:runtime-generation:$generation:state"
  lock_key="qiu:runtime-generation:$generation:lock"
  result="$(redis_command EVAL '
    if redis.call("GET", KEYS[1]) ~= ARGV[1] then return -1 end
    redis.call("DEL", KEYS[2], KEYS[3])
    redis.call("DEL", KEYS[1])
    return 1
  ' 3 "$owner_key" "$state_key" "$lock_key" "$owner")"
  [ "$result" = 1 ] || { echo 'old Redis generation ownership mismatch; cleanup refused' >&2; return 1; }
}

write_state() {
  local phase="$1" backup="$2" commit="$3" temporary="$state_file.$$.tmp"
  jq -n --arg phase "$phase" --arg backup "$backup" --arg commit "$commit" \
    --arg at "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    '{schema_version:"qiu.live-cutover.state.v1",phase:$phase,backup:$backup,commit:$commit,recorded_at:$at}' > "$temporary" || return 1
  chmod 600 "$temporary" || return 1
  mv "$temporary" "$state_file"
}

activate_generation() {
  local manifest="$1" market_sha frontdoor_sha schema processes
  schema="$(jq -r '.schema_version' "$manifest")"
  market_sha="$(jq -r '.binary_sha256' "$manifest")"; frontdoor_sha="$(jq -r '.frontdoor_sha256 // empty' "$manifest")"
  if [ "$schema" = 'qiu.d1.active-release.v2' ]; then
    write_generation "$manifest" "$runtime/run/committed-generation.json" false '{}' || return 1
  fi
  start_label "$frontdoor_label" || return 1
  wait_label_running "$frontdoor_label" || return 1
  if [ "$schema" = 'qiu.d1.active-release.v2' ]; then wait_frontdoor_binary "$frontdoor_sha" || return 1; fi
  start_label "$stack_label" || return 1
  wait_label_running "$stack_label" || return 1
  for label in com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker; do start_label "$label" || return 1; done
  if [ "$schema" = 'qiu.d1.active-release.v2' ]; then
    for label in com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker; do
      wait_label_running "$label" || return 1
      wait_label_binary "$label" "$market_sha" || return 1
    done
    probe_market_contract "$manifest" 18080 false 200 || return 1
    probe_market_contract "$manifest" 18084 true 503 || return 1
    processes="$(capture_generation_processes false)" || return 1
    claim_redis_generation "$manifest" || return 1
    write_generation "$manifest" "$runtime/run/committed-generation.json" true "$processes" || return 1
    probe_market_contract "$manifest" 18084 true 200 || return 1
    start_label 'com.qiu-market.live.api-tunnel' || return 1
    wait_label_running 'com.qiu-market.live.api-tunnel' || return 1
    processes="$(capture_generation_processes true)" || return 1
    write_generation "$manifest" "$runtime/run/committed-generation.json" true "$processes" || return 1
    probe_market_contract "$manifest" 18084 true 200 || return 1
    return
  fi
  for label in com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker; do
    wait_label_binary "$label" "$market_sha" || return 1
  done
  start_label 'com.qiu-market.live.api-tunnel' || return 1
  wait_label_running 'com.qiu-market.live.api-tunnel'
}

stop_candidate_generation() {
  local label
  for label in com.qiu-market.live.api-tunnel com.qiu-market.live.crawler com.qiu-market.live.dex \
    com.qiu-market.live.worker "$stack_label" "$frontdoor_label"; do
    pause_label "$label" || return 1
    wait_label_stopped "$label" || return 1
  done
  if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" != true ]; then
    for port in 6389 18080 18081 18083 18084; do
      [ -z "$(listener_pid "$port")" ] || return 1
    done
  fi
}

perform_cutover() {
  local manifest="$1" backup="$state_dir/backup-$(date '+%s')-$$" old_manifest old_generation commit
  [ "$execute_flag" = '--execute' ] || { echo 'cutover requires --execute' >&2; return 2; }
  selector_preflight "$manifest"
  acquire_lock
  backup_runtime "$backup"
  old_manifest="$backup/config/active-release.json"; old_generation="$backup/run/committed-generation.json"
  if [ "$(jq -r '.schema_version // empty' "$old_manifest" 2>/dev/null || true)" = 'qiu.d1.active-release.v2' ]; then
    [ "$(jq -r '.redis_generation' "$old_manifest")" != "$(jq -r '.redis_generation' "$manifest")" ] || {
      echo 'candidate must claim a distinct Redis generation' >&2
      return 1
    }
  fi
  commit="$(jq -r '.commit' "$manifest")"
  write_state activating "$backup" "$commit"
  rollback_needed=true
  rollback_on_failure() {
    local original_status="$?" rollback_ok
    trap - ERR
    if [ "$rollback_needed" = true ]; then
      rollback_ok=true
      stop_candidate_generation >/dev/null 2>&1 || rollback_ok=false
      if [ "$rollback_ok" = true ]; then restore_runtime "$backup" || rollback_ok=false; fi
      if [ "$rollback_ok" = true ]; then
        activate_generation "$old_manifest" >/dev/null 2>&1 || rollback_ok=false
      fi
      if [ "$rollback_ok" = true ]; then
        release_uncommitted_redis_generation "$manifest" >/dev/null 2>&1 || rollback_ok=false
      fi
      if [ "$rollback_ok" = true ]; then
        write_state rolled-back "$backup" "$commit" || rollback_ok=false
      fi
      if [ "$rollback_ok" != true ]; then
        pause_label 'com.qiu-market.live.api-tunnel' >/dev/null 2>&1 || true
        write_state rollback-failed "$backup" "$commit" || true
      fi
    fi
    [ "$original_status" -ne 0 ] || original_status=1
    return "$original_status"
  }
  trap rollback_on_failure ERR
  pause_label 'com.qiu-market.live.api-tunnel'
  [ "${QIU_MARKET_LIVE_CUTOVER_FAIL_AT:-}" != after_tunnel_stop ]
  install_runtime_consumers
  install_candidate_release_manifest "$manifest"
  install -m 600 "$manifest" "$runtime/config/active-release.next"; mv "$runtime/config/active-release.next" "$runtime/config/active-release.json"
  activate_generation "$manifest"
  [ "${QIU_MARKET_LIVE_CUTOVER_FAIL_AT:-}" != after_restart ]
  verify_old_processes_stopped "$old_generation"
  if [ -f "$old_manifest" ]; then
    old_pidfile="$(jq -r '.redis_pidfile // empty' "$old_manifest")"
    old_pid="$(jq -r '.processes.redis // empty' "$old_generation" 2>/dev/null || true)"
    if [ -n "$old_pidfile" ] && [ -n "$old_pid" ]; then owner_checked_remove_pidfile "$old_pidfile" "$old_pid" "${QIU_MARKET_LIVE_REDIS_PORT:-6389}"; fi
  fi
  write_state awaiting-preview-acceptance "$backup" "$commit"
  cleanup_old_redis_generation "$old_manifest" "$old_generation"
  rollback_needed=false
  trap - ERR
  printf 'live_cutover=ready commit=%s tunnel_target=http://127.0.0.1:18084 production_promoted=false\n' "$commit"
}

case "$action" in
  preflight) selector_preflight "$candidate"; echo 'live_cutover_preflight=passed mutated=false' ;;
  cutover) perform_cutover "$candidate" ;;
  pidfile-cleanup)
    [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ] || { echo 'pidfile cleanup fixture action is test-only' >&2; exit 64; }
    owner_checked_remove_pidfile "$candidate" "$execute_flag" "${4:-6389}"
    ;;
  status) [ -f "$state_file" ] && jq . "$state_file" || echo 'live_cutover_status=inactive' ;;
  *) echo 'usage: live-cutover.sh preflight <manifest> | cutover <manifest> --execute | status' >&2; exit 64 ;;
esac
