#!/bin/bash
set -euo pipefail
umask 077

runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
# shellcheck disable=SC1090
source "$runtime/ops/release-selector"
load_active_release
if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  printf 'frontdoor=%s listen=127.0.0.1:18084 upstream=http://127.0.0.1:18080 generation=%s\n' \
    "$active_release_frontdoor_binary_path" "$active_release_generation_id"
  exit 0
fi

ops="$runtime/ops"
frontdoor_pid=''
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ -n "$frontdoor_pid" ] && kill -0 "$frontdoor_pid" 2>/dev/null; then
    owner="$(lsof -nP -iTCP:18084 -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true)"
    [ "$owner" != "$frontdoor_pid" ] || kill -TERM "$frontdoor_pid" 2>/dev/null || true
  fi
  "$ops/stop" >/dev/null 2>&1 || status=1
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

"$ops/d1-launch-preflight"
for port in 6389 18080 18081 18083 18084; do
  ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 || { echo "D1 live stack port already owned: $port" >&2; exit 1; }
done
"$ops/start"
"$active_release_frontdoor_binary_path" \
  --listen 127.0.0.1:18084 --upstream http://127.0.0.1:18080 \
  --manifest "$qiu_active_release_file" \
  --generation "$runtime/run/committed-generation.json" \
  >> "$runtime/logs/live-frontdoor.out.log" 2>> "$runtime/logs/live-frontdoor.err.log" &
frontdoor_pid=$!
temporary="$runtime/run/frontdoor.pid.$$.tmp"
printf '%s\n' "$frontdoor_pid" > "$temporary"; chmod 600 "$temporary"; mv "$temporary" "$runtime/run/frontdoor.pid"
for _ in $(jot 60); do
  owner="$(lsof -nP -iTCP:18084 -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true)"
  [ "$owner" != "$frontdoor_pid" ] || break
  kill -0 "$frontdoor_pid" 2>/dev/null || { echo 'pure frontdoor exited during startup' >&2; exit 1; }
  sleep 0.25
done
curl --fail --silent --max-time 2 http://127.0.0.1:18080/healthz >/dev/null
[ "$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 2 http://127.0.0.1:18084/healthz)" = 503 ] || {
  echo 'pure frontdoor did not remain drained before generation commit' >&2
  exit 1
}

while :; do
  load_active_release
  [ "$(jq -r '.generation_id' "$runtime/run/committed-generation.json")" = "$active_release_generation_id" ] || exit 69
  for pair in 'redis:6389' 'api:18080' 'trading:18081' 'rpc:18083' 'frontdoor:18084'; do
    role="${pair%%:*}"; port="${pair##*:}"
    if [ "$role" = frontdoor ]; then pid="$frontdoor_pid"; else pid="$(<"$runtime/run/$role.pid")"; fi
    kill -0 "$pid" 2>/dev/null || exit 70
    owner="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true)"
    [ "$owner" = "$pid" ] || exit 71
  done
  sleep 5
done
