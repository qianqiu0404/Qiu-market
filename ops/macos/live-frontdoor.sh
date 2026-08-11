#!/bin/bash
set -euo pipefail
umask 077

runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
# shellcheck disable=SC1090
source "$runtime/ops/release-selector"
load_active_release
if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  printf 'pure_frontdoor=%s listen=127.0.0.1:18084 upstream=http://127.0.0.1:18080 generation=%s\n' \
    "$active_release_frontdoor_binary_path" "$active_release_generation_id"
  exit 0
fi

! lsof -nP -iTCP:18084 -sTCP:LISTEN >/dev/null 2>&1 || { echo 'pure frontdoor port already owned' >&2; exit 1; }
exec "$active_release_frontdoor_binary_path" \
  --listen 127.0.0.1:18084 --upstream http://127.0.0.1:18080 \
  --manifest "$qiu_active_release_file" \
  --generation "$runtime/run/committed-generation.json"
