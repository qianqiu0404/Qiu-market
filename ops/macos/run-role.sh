#!/usr/bin/env bash
set -euo pipefail

role="${1:-}"
runtime_root="${2:-}"
case "$role" in
  api|trading|crawler|worker|dex|dw) ;;
  *)
    echo "unsupported Qiu Market managed role: $role" >&2
    exit 2
    ;;
esac
if [ -z "$runtime_root" ] || [ ! -d "$runtime_root/ops/macos" ]; then
  echo "Qiu Market immutable runtime root is unavailable" >&2
  exit 1
fi

support_dir="$HOME/Library/Application Support/Qiu Market"
production_env="$support_dir/production.env"
database_env="${QIU_MARKET_DATABASE_ENV_FILE:-$support_dir/database.env}"
binary="$support_dir/bin/market-services"
if [ ! -x "$binary" ]; then
  echo "managed Qiu Market binary is missing: $binary" >&2
  exit 1
fi
if [ ! -f "$production_env" ]; then
  echo "private production environment is missing: $production_env" >&2
  exit 1
fi
if [ ! -f "$database_env" ]; then
  echo "private database environment is missing: $database_env" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$database_env"
# shellcheck disable=SC1090
source "$production_env"
set +a

if [ -d "$runtime_root/migrations" ]; then
  export MARKET_MIGRATIONS_DIR="$runtime_root/migrations"
fi

script_dir="$runtime_root/ops/macos"
# shellcheck disable=SC1091
source "$script_dir/proxy-env.sh"
qiu_export_system_proxy

cd "$runtime_root"
exec "$binary" "$role"
