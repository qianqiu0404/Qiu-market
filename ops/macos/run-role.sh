#!/usr/bin/env bash
set -euo pipefail

role="${1:-}"
repo_root="${2:-}"
case "$role" in
  api|trading|crawler|worker|dex) ;;
  *)
    echo "unsupported Qiu Market managed role: $role" >&2
    exit 2
    ;;
esac
if [ -z "$repo_root" ] || [ ! -f "$repo_root/.env" ]; then
  echo "Qiu Market repository or local .env is unavailable" >&2
  exit 1
fi

support_dir="$HOME/Library/Application Support/Qiu Market"
production_env="$support_dir/production.env"
binary="$support_dir/bin/market-services"
if [ ! -x "$binary" ]; then
  echo "managed Qiu Market binary is missing: $binary" >&2
  exit 1
fi
if [ ! -f "$production_env" ]; then
  echo "private production environment is missing: $production_env" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$repo_root/.env"
# shellcheck disable=SC1090
source "$production_env"
set +a

cd "$repo_root"
exec "$binary" "$role"
