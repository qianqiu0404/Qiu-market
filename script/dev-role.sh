#!/usr/bin/env bash
set -euo pipefail

role="${1:-}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_dir="/tmp/s78-market-services-${UID}"
mkdir -p "$runtime_dir"

case "$role" in
  api|trading|rpc|crawler|worker|dex|dw|frontend) ;;
  *)
    echo "Unknown development role: $role" >&2
    exit 2
    ;;
esac

pid_file="$runtime_dir/$role.pid"
log_file="$runtime_dir/$role.log"
if [ -f "$pid_file" ]; then
  existing_pid="$(tr -dc '0-9' < "$pid_file")"
  if [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null; then
    echo "$role is already running as PID $existing_pid" >&2
    exit 1
  fi
fi

cd "$root_dir"
printf '%s\n' "$$" > "$pid_file"
# The writer reopens the active file after every bounded rotation. A single
# long-running role therefore cannot keep appending to a renamed archive.
exec > >(bash "$root_dir/script/dev-log-writer.sh" "$log_file") 2>&1

role_title="$(printf '%s' "$role" | tr '[:lower:]' '[:upper:]')"
printf '\033]1;%s\007' "S78 $role_title"
echo "[$(date '+%Y-%m-%d %H:%M:%S')] starting $role"

if [ "$role" = "frontend" ]; then
  cd frontend
  exec ./node_modules/.bin/vite
fi

# shellcheck disable=SC1091
source .env
if [ "$role" = "dex" ]; then
  # Local development can run read-only AMM previews without requiring paid
  # endpoints in .env. A direct/production `market-services dex` launch keeps
  # this fallback disabled unless it is explicitly configured.
  : "${MARKET_DEX_PUBLIC_FALLBACK:=${S78_DEX_PUBLIC_FALLBACK:-1}}"
  export MARKET_DEX_PUBLIC_FALLBACK
fi
exec ./market-services "$role"
