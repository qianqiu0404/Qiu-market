#!/bin/bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d /tmp/qiu-market-live-role.XXXXXX)"
runtime="$fixture/runtime"
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  find "$fixture" -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

install -d -m 700 "$runtime/ops" "$runtime/config" "$runtime/secrets" \
  "$runtime/bin" "$runtime/source" "$runtime/run"

selector="$runtime/ops/release-selector"
cat > "$selector" <<'SELECTOR'
release_private_file() {
  [ -f "$1" ] && [ ! -L "$1" ] &&
    [ "$(stat -f '%u:%Lp' "$1" 2>/dev/null || true)" = "$(id -u):600" ]
}
load_active_release() {
  active_release_commit='1111111111111111111111111111111111111111'
  active_release_binary_sha256='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  active_release_data_mode='live'
  active_release_source_path="${QIU_MARKET_LIVE_RUNTIME:?}/source"
  active_release_binary_path="${QIU_MARKET_LIVE_RUNTIME:?}/bin/assert-role-env"
  export active_release_commit active_release_binary_sha256 active_release_data_mode
  export active_release_source_path active_release_binary_path
}
SELECTOR
chmod 700 "$selector"

cat > "$runtime/config/database.env" <<'ENV'
MARKET_MASTER_DB_HOST=127.0.0.1
MARKET_MASTER_DB_PORT=5432
MARKET_MASTER_DB_USER=fixture
MARKET_MASTER_DB_PASSWORD=fixture-only
MARKET_MASTER_DB_NAME=s78_market
ENV
printf '%s\n' 'fixture-only' > "$runtime/secrets/redis-password"
chmod 600 "$runtime/config/database.env" "$runtime/secrets/redis-password"

cat > "$runtime/bin/assert-role-env" <<'BINARY'
#!/bin/bash
set -euo pipefail
[ "$MARKET_DEX_PUBLIC_FALLBACK" = "$(<"${QIU_MARKET_LIVE_RUNTIME:?}/run/expected-fallback")" ]
[ "$MARKET_ETHEREUM_RPC_URL" = '' ]
[ "$MARKET_BSC_RPC_URL" = '' ]
[ "$MARKET_UNISWAP_V3_SUBGRAPH_URL" = '' ]
[ "$MARKET_PANCAKE_V3_SUBGRAPH_URL" = '' ]
BINARY
chmod 700 "$runtime/bin/assert-role-env"

live_role="$repo_root/ops/macos/live-role.sh"
export QIU_MARKET_LIVE_RUNTIME="$runtime"

printf '%s\n' 'MARKET_DEX_PUBLIC_FALLBACK=true' > "$runtime/source/.env"
printf '%s\n' false > "$runtime/run/expected-fallback"
chmod 600 "$runtime/source/.env" "$runtime/run/expected-fallback"
"$live_role" dex

provider_config="$runtime/config/provider-readonly.env"
printf '%s\n' 'MARKET_DEX_PUBLIC_FALLBACK=true' > "$provider_config"
chmod 600 "$provider_config"
printf '%s\n' true > "$runtime/run/expected-fallback"
"$live_role" dex

printf '%s\n' false > "$runtime/run/expected-fallback"
"$live_role" crawler

chmod 644 "$provider_config"
if "$live_role" dex >/dev/null 2>&1; then
  echo 'world-readable provider config was accepted' >&2
  exit 1
fi
chmod 600 "$provider_config"

mv "$provider_config" "$provider_config.target"
ln -s "$provider_config.target" "$provider_config"
if "$live_role" dex >/dev/null 2>&1; then
  echo 'symlink provider config was accepted' >&2
  exit 1
fi
find "$provider_config" -maxdepth 0 -type l -delete
mv "$provider_config.target" "$provider_config"

sentinel='must-not-appear-provider-config-value'
printf 'MARKET_ETHEREUM_RPC_URL=%s\n' "$sentinel" > "$provider_config"
chmod 600 "$provider_config"
invalid_output="$fixture/invalid-output"
if "$live_role" dex >"$invalid_output" 2>&1; then
  echo 'unknown provider config key was accepted' >&2
  exit 1
fi
! grep -F "$sentinel" "$invalid_output" >/dev/null

printf '%s\n' 'MARKET_DEX_PUBLIC_FALLBACK=true' 'MARKET_DEX_PUBLIC_FALLBACK=false' > "$provider_config"
if "$live_role" dex >/dev/null 2>&1; then
  echo 'duplicate provider fallback config was accepted' >&2
  exit 1
fi

echo 'Live role provider read-only fallback boundary fixtures passed.'
