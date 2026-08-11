#!/bin/bash
set -euo pipefail
umask 077

runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
# shellcheck disable=SC1090
source "$runtime/ops/release-selector"
load_active_release
target="$(jq -r '.tunnel_target' "$qiu_active_release_file")"
[ "$target" = 'http://127.0.0.1:18084' ] || { echo 'live tunnel refuses direct API target' >&2; exit 65; }
jq -e --arg generation "$active_release_generation_id" --arg owner "$active_release_generation_owner_token" --arg commit "$active_release_commit" '
  .schema_version=="qiu.d1.committed-generation.v2" and .ready==true and
  .generation_id==$generation and .owner_token==$owner and .commit==$commit and
  .data_mode=="live" and .frontdoor_port==18084 and .upstream_port==18080 and
  .tunnel_target=="http://127.0.0.1:18084"
' "$runtime/run/committed-generation.json" >/dev/null || { echo 'live tunnel generation identity is not committed' >&2; exit 66; }
if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  printf 'tunnel_target=%s generation=%s ready=%s\n' "$target" "$active_release_generation_id" \
    "$(jq -r '.ready' "$runtime/run/committed-generation.json")"
  exit 0
fi

credentials="$runtime/run/quick-tunnel/credentials.json"
cloudflared="$runtime/bin/cloudflared"
release_private_exec "$cloudflared" || { echo 'managed cloudflared binary is unavailable' >&2; exit 67; }
release_private_file "$credentials" || { echo 'managed tunnel credential is unavailable or unsafe' >&2; exit 68; }
curl --fail --silent --show-error --max-time 3 "$target/healthz" >/dev/null
exec "$cloudflared" tunnel --no-autoupdate --credentials-file "$credentials" \
  --url "$target" --protocol quic run 7d2ac5be-ff1c-4d32-b472-579d63078992
