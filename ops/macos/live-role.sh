#!/bin/bash
set -euo pipefail
umask 077

runtime="${QIU_MARKET_LIVE_RUNTIME:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
# shellcheck disable=SC1090
source "$runtime/ops/release-selector"
load_active_release

role="${1:-}"
case "$role" in crawler|dex|worker) ;; *) echo 'unsupported Qiu Market live read-only role' >&2; exit 64 ;; esac
if [ "${QIU_MARKET_LIVE_CUTOVER_TEST_MODE:-false}" = true ]; then
  printf 'role=%s commit=%s binary_sha256=%s data_mode=%s\n' \
    "$role" "$active_release_commit" "$active_release_binary_sha256" "$active_release_data_mode"
  exit 0
fi

database_env="$runtime/config/database.env"
redis_secret_file="$runtime/secrets/redis-password"
for file in "$database_env" "$redis_secret_file"; do
  release_private_file "$file" || { echo 'required private runtime file is unavailable or unsafe' >&2; exit 65; }
done
set -a
# shellcheck disable=SC1090
source "$database_env"
set +a
: "${MARKET_MASTER_DB_HOST:?}" "${MARKET_MASTER_DB_PORT:?}" "${MARKET_MASTER_DB_USER:?}"
: "${MARKET_MASTER_DB_PASSWORD:?}" "${MARKET_MASTER_DB_NAME:?}"
[ "$MARKET_MASTER_DB_HOST" = '127.0.0.1' ] && [ "$MARKET_MASTER_DB_PORT" = '5432' ] && [ "$MARKET_MASTER_DB_NAME" = 's78_market' ]

export MARKET_MIGRATIONS_DIR="$active_release_source_path/migrations"
export MARKET_HTTP_HOST='127.0.0.1' MARKET_HTTP_PORT='18080'
export MARKET_RPC_HOST='127.0.0.1' MARKET_RPC_PORT='18083'
export MARKET_METRIC_HOST='127.0.0.1' MARKET_METRIC_PORT='18082'
export MARKET_REDIS_ADDRESS='127.0.0.1:6389' MARKET_REDIS_DB_INDEX='15'
export MARKET_REDIS_PASSWORD="$(<"$redis_secret_file")"
export MARKET_MULTI_VENUE_ENABLED='true'
export MARKET_ETHEREUM_RPC_URL='' MARKET_BSC_RPC_URL=''
export MARKET_UNISWAP_V3_SUBGRAPH_URL='' MARKET_PANCAKE_V3_SUBGRAPH_URL=''
export MARKET_DEX_PUBLIC_FALLBACK='false' MARKET_RESEARCH_SIGNALS_ENABLED='false' MARKET_DORIS_HOST=''
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy
export NO_PROXY='127.0.0.1,localhost,::1' no_proxy='127.0.0.1,localhost,::1'
cd "$active_release_source_path"
exec "$active_release_binary_path" "$role"
