#!/bin/bash
set -euo pipefail
umask 077

die() { echo "$1" >&2; exit 64; }
private_file() {
  [ -f "$1" ] && [ ! -L "$1" ] &&
    [ "$(stat -f '%u:%Lp' "$1" 2>/dev/null || true)" = "$(id -u):600" ]
}
canonical_directory() {
  [ "${1#/}" != "$1" ] || return 1
  [ -d "$1" ] && [ ! -L "$1" ] || return 1
  (cd "$1" && pwd -P)
}
process_matches() {
  local pid="$1" expected="$2" role="$3" command owner
  [ "$role" != gateway ] || role=trading-gateway
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  owner="$(ps -p "$pid" -o uid= | tr -d ' ')"
  [ "$owner" = "$(id -u)" ] || return 1
  command="$(ps -p "$pid" -o command=)"
  case "$command" in *"$expected"*" $role"*) return 0 ;; *) return 1 ;; esac
}
wait_process() {
  local pid="$1" expected="$2" role="$3"
  for _ in $(seq 1 100); do
    process_matches "$pid" "$expected" "$role" && return 0
    sleep 0.1
  done
  return 1
}
wait_http() {
  for _ in $(seq 1 100); do
    if curl --noproxy '*' -fsS --max-time 1 "http://127.0.0.1:$http_port/healthz" >/dev/null 2>&1 &&
      curl --noproxy '*' -fsS --max-time 1 \
        "http://127.0.0.1:$http_port/api/v1/trading/markets/BTC-USDT/status" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}
wait_tcp() {
  local port="$1"
  for _ in $(seq 1 100); do
    nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}
stop_role() {
  local role="$1" required="${2:-true}"
  local pidfile="$run/$role.pid" ownerfile="$run/$role.owner" pid expected
  if ! private_file "$pidfile" || ! private_file "$ownerfile"; then
    [ "$required" = false ] && return 0
    return 1
  fi
  pid="$(<"$pidfile")"
  expected="$(<"$ownerfile")"
  process_matches "$pid" "$expected" "$role" || return 1
  kill -TERM "$pid"
  for _ in $(seq 1 100); do
    kill -0 "$pid" 2>/dev/null || {
      find "$pidfile" "$ownerfile" -maxdepth 0 -type f -delete
      return 0
    }
    sleep 0.1
  done
  return 1
}

action="${1:-}"
requested_runtime="${2:-}"
[ -n "$action" ] && [ -n "$requested_runtime" ] || die 'usage: practice-local.sh start|status|stop <absolute-runtime-dir> [executable]'
runtime="$(canonical_directory "$requested_runtime")" || die 'practice runtime must be an existing absolute non-symlink directory'
case "$runtime" in
  "$HOME/Library/Application Support/Qiu Market"/*|*/d1-candidate|*/d1-candidate/*)
    die 'live Qiu Market runtime is never a practice target'
    ;;
esac
run="$runtime/run"
logs="$runtime/logs"
grpc_port="${QIU_T1_GRPC_PORT:-19094}"
http_port="${QIU_T1_HTTP_PORT:-19092}"
frontend_origin="${QIU_T1_FRONTEND_ORIGIN:-http://127.0.0.1:5174}"

case "$action" in
  start)
    executable="${3:-}"
    [ "${executable#/}" != "$executable" ] && [ -f "$executable" ] && [ -x "$executable" ] && [ ! -L "$executable" ] ||
      die 'start requires an absolute executable regular non-symlink file'
    for file in "$runtime/config/state-postgres-dsn" \
      "$runtime/config/reference-postgres-dsn" "$runtime/secrets/cursor-hmac-current"; do
      private_file "$file" || die 'required practice private file is missing or unsafe'
    done
    install -d -m 700 "$run" "$logs"
    for role in trading gateway; do
      if private_file "$run/$role.pid" && private_file "$run/$role.owner" &&
        process_matches "$(<"$run/$role.pid")" "$(<"$run/$role.owner")" "$role"; then
        die 'practice process is already running'
      fi
    done
    [[ "$grpc_port" =~ ^[1-9][0-9]{3,4}$ ]] && [[ "$http_port" =~ ^[1-9][0-9]{3,4}$ ]] ||
      die 'invalid isolated practice port'
    [ "$grpc_port" != 9094 ] && [ "$http_port" != 9092 ] && [ "$grpc_port" != "$http_port" ] ||
      die 'live/default or duplicate ports are not isolated practice targets'
    if [[ ! "$frontend_origin" =~ ^http://(127\.0\.0\.1|\[::1\]):([1-9][0-9]{0,4})$ ]]; then
      die 'practice frontend origin must be http with an explicit IP loopback host and port'
    fi
    frontend_port="${BASH_REMATCH[2]}"
    if [ "$frontend_port" -gt 65535 ] || [ "$frontend_port" = 80 ] ||
      [ "$frontend_port" = 443 ] || [ "$frontend_port" = 9092 ] ||
      [ "$frontend_port" = 9094 ] || [ "$frontend_port" = "$http_port" ] ||
      [ "$frontend_port" = "$grpc_port" ]; then
      die 'practice frontend origin uses a reserved, invalid, or backend port'
    fi
    if nc -z 127.0.0.1 "$grpc_port" >/dev/null 2>&1 || nc -z 127.0.0.1 "$http_port" >/dev/null 2>&1; then
      die 'isolated practice port is already in use'
    fi
    cursor="$(<"$runtime/secrets/cursor-hmac-current")"
    [ -n "$cursor" ] || die 'practice cursor key is empty'
    for role in trading gateway; do
      : > "$logs/$role.out.log"
      : > "$logs/$role.err.log"
      chmod 600 "$logs/$role.out.log" "$logs/$role.err.log"
    done
    unset MARKET_TRADING_GITHUB_CLIENT_ID MARKET_TRADING_GITHUB_CLIENT_SECRET \
      MARKET_TRADING_GITHUB_REDIRECT_URL
    export MARKET_MIGRATIONS_DIR="$runtime/migrations"
    export MARKET_RPC_HOST=127.0.0.1 MARKET_RPC_PORT=19083
    export MARKET_HTTP_HOST=127.0.0.1 MARKET_HTTP_PORT="$http_port"
    export MARKET_METRIC_HOST=127.0.0.1 MARKET_METRIC_PORT=19082
    export MARKET_MASTER_DB_HOST=127.0.0.1 MARKET_MASTER_DB_PORT=1
    export MARKET_MASTER_DB_USER=unused MARKET_MASTER_DB_PASSWORD=unused MARKET_MASTER_DB_NAME=unused
    export MARKET_REDIS_ADDRESS=127.0.0.1:1 MARKET_REDIS_DB_INDEX=0
    export MARKET_TRADING_GRPC_ADDR="127.0.0.1:$grpc_port"
    export MARKET_TRADING_PRACTICE_MODE=true MARKET_TRADING_LOCAL_AUTH=true
    export MARKET_TRADING_DEMO_MAKER_ENABLED=true
    export MARKET_TRADING_SECURE_COOKIES=false
    export MARKET_TRADING_ALLOWED_ORIGINS="$frontend_origin"
    export MARKET_TRADING_STATE_DSN_FILE="$runtime/config/state-postgres-dsn"
    export MARKET_TRADING_REFERENCE_DSN_FILE="$runtime/config/reference-postgres-dsn"
    export MARKET_TRADING_CURSOR_HMAC_CURRENT="$cursor"

    nohup "$executable" trading >"$logs/trading.out.log" 2>"$logs/trading.err.log" </dev/null &
    trading_pid=$!
    printf '%s\n' "$trading_pid" > "$run/trading.pid"
    printf '%s\n' "$executable" > "$run/trading.owner"
    chmod 600 "$run/trading.pid" "$run/trading.owner"
    if ! wait_process "$trading_pid" "$executable" trading || ! wait_tcp "$grpc_port"; then
      stop_role trading false || true
      die 'practice trading process did not remain running'
    fi

    nohup "$executable" trading-gateway >"$logs/gateway.out.log" 2>"$logs/gateway.err.log" </dev/null &
    gateway_pid=$!
    printf '%s\n' "$gateway_pid" > "$run/gateway.pid"
    printf '%s\n' "$executable" > "$run/gateway.owner"
    chmod 600 "$run/gateway.pid" "$run/gateway.owner"
    if ! wait_process "$gateway_pid" "$executable" trading-gateway || ! wait_http; then
      stop_role gateway false || true
      stop_role trading false || true
      die 'practice trading gateway did not become healthy'
    fi
    echo "practice stack started trading_pid=$trading_pid gateway_pid=$gateway_pid http=127.0.0.1:$http_port grpc=127.0.0.1:$grpc_port"
    ;;
  status)
    for role in trading gateway; do
      private_file "$run/$role.pid" && private_file "$run/$role.owner" || die 'practice stack ownership is unavailable'
      process_matches "$(<"$run/$role.pid")" "$(<"$run/$role.owner")" "$role" ||
        die 'practice stack process is not running under recorded ownership'
    done
    wait_tcp "$grpc_port" || die 'practice trading gRPC is unavailable'
    wait_http || die 'practice trading gateway health is unavailable'
    echo "practice stack running trading_pid=$(<"$run/trading.pid") gateway_pid=$(<"$run/gateway.pid") http=127.0.0.1:$http_port grpc=127.0.0.1:$grpc_port"
    ;;
  stop)
    stop_ok=true
    stop_role gateway || stop_ok=false
    stop_role trading || stop_ok=false
    if nc -z 127.0.0.1 "$http_port" >/dev/null 2>&1 || nc -z 127.0.0.1 "$grpc_port" >/dev/null 2>&1; then
      stop_ok=false
    fi
    [ "$stop_ok" = true ] || die 'practice stack ownership mismatch or bounded stop failure'
    echo 'practice stack stopped'
    ;;
  *) die 'unsupported practice action' ;;
esac
