#!/bin/bash
set -euo pipefail
umask 077

runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
# shellcheck disable=SC1090
source "$runtime/ops/release-selector"
load_active_release
if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  printf 'business_stack=true commit=%s ports=6389,18080,18081,18083 generation=%s\n' \
    "$active_release_commit" "$active_release_generation_id"
  exit 0
fi

ops="$runtime/ops"
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  "$ops/stop" >/dev/null 2>&1 || status=1
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

"$ops/d1-launch-preflight"
for port in 6389 18080 18081 18083; do
  ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 || {
    echo "D1 live business port already owned: $port" >&2
    exit 1
  }
done
"$ops/start"
curl --fail --silent --max-time 2 http://127.0.0.1:18080/healthz >/dev/null

while :; do
  load_active_release
  [ "$(jq -r '.generation_id' "$runtime/run/committed-generation.json")" = "$active_release_generation_id" ] || exit 69
  for pair in 'redis:6389' 'api:18080' 'trading:18081' 'rpc:18083'; do
    role="${pair%%:*}"; port="${pair##*:}"; pid="$(<"$runtime/run/$role.pid")"
    kill -0 "$pid" 2>/dev/null || exit 70
    owner="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -Fp 2>/dev/null | awk '/^p/{sub(/^p/,"");print;exit}' || true)"
    [ "$owner" = "$pid" ] || exit 71
  done
  sleep 5
done
