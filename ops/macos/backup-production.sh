#!/usr/bin/env bash
set -euo pipefail
umask 077

mode="${1:-full}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "$repo_root/ops/macos/production-lib.sh"
qiu_load_production_environment "$repo_root"
qiu_require_private_environment

for command_name in pg_dump shasum; do
  qiu_require_command "$command_name"
done

database_name="$QIU_MARKET_DB_NAME"
if [[ ! "$database_name" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "Unsafe database name for backup filename: $database_name" >&2
  exit 1
fi
support_dir="$QIU_MARKET_SUPPORT_DIR"
backup_root="$support_dir/backups"
stamp="$(date '+%Y%m%d-%H%M%S')-$$"

install -d -m 700 \
  "$backup_root/full" \
  "$backup_root/hourly-trading" \
  "$backup_root/manifests" \
  "$backup_root/locks"

lock_dir="$backup_root/locks/$mode.lock"
temporary_target=""
temporary_manifest=""
owns_lock=false
cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  if [ -n "$temporary_target" ] && [ -f "$temporary_target" ]; then
    find "$temporary_target" -maxdepth 0 -type f -delete 2>/dev/null || true
  fi
  if [ -n "$temporary_manifest" ] && [ -f "$temporary_manifest" ]; then
    find "$temporary_manifest" -maxdepth 0 -type f -delete 2>/dev/null || true
  fi
  if [ "$owns_lock" = "true" ] && [ -d "$lock_dir" ]; then
    rmdir "$lock_dir" 2>/dev/null || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! mkdir "$lock_dir" 2>/dev/null; then
  echo "Another Qiu Market $mode backup is already running." >&2
  exit 1
fi
owns_lock=true

case "$mode" in
  full)
    target="$backup_root/full/$database_name-$stamp.dump"
    temporary_target="$target.partial"
    pg_dump -Fc -Z9 \
      --host="$QIU_MARKET_DB_HOST" \
      --port="$QIU_MARKET_DB_PORT" \
      --username="$QIU_MARKET_DB_USER" \
      --no-password \
      --dbname="$database_name" \
      --file="$temporary_target"
    keep=2
    directory="$backup_root/full"
    ;;
  trading)
    target="$backup_root/hourly-trading/trading-$stamp.dump"
    temporary_target="$target.partial"
    pg_dump -Fc -Z9 \
      --host="$QIU_MARKET_DB_HOST" \
      --port="$QIU_MARKET_DB_PORT" \
      --username="$QIU_MARKET_DB_USER" \
      --no-password \
      --dbname="$database_name" \
      --table='trading_*' \
      --file="$temporary_target"
    keep=24
    directory="$backup_root/hourly-trading"
    ;;
  *)
    echo "Usage: $0 full|trading" >&2
    exit 2
    ;;
esac

chmod 600 "$temporary_target"
manifest="$backup_root/manifests/$(basename "${target%.dump}").sha256"
temporary_manifest="$manifest.partial"
digest="$(qiu_sha256 "$temporary_target")"
printf '%s  %s\n' "$digest" "$target" > "$temporary_manifest"
chmod 600 "$temporary_manifest"
mv "$temporary_target" "$target"
temporary_target=""
mv "$temporary_manifest" "$manifest"
temporary_manifest=""

if [ "${QIU_MARKET_DEFER_BACKUP_RETENTION:-false}" != "true" ]; then
  backups=()
  while IFS= read -r backup; do
    backups+=("$backup")
  done < <(find "$directory" -maxdepth 1 -type f -name '*.dump' -print | sort -r)
  if [ "${#backups[@]}" -gt "$keep" ]; then
    for expired in "${backups[@]:$keep}"; do
      manifest_path="$backup_root/manifests/$(basename "${expired%.dump}").sha256"
      find "$expired" -maxdepth 0 -type f -delete
      if [ -f "$manifest_path" ]; then
        find "$manifest_path" -maxdepth 0 -type f -delete
      fi
    done
  fi
fi

echo "$mode backup ready: $target" >&2
printf '%s\n' "$target"
