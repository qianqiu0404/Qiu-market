#!/usr/bin/env bash
set -euo pipefail
umask 077

mode="${1:-full}"
database_name="${MARKET_DB_NAME:-s78_market}"
support_dir="/Users/xiuqiu/Library/Application Support/Qiu Market"
backup_root="$support_dir/backups"
stamp="$(date '+%Y%m%d-%H%M%S')"

install -d -m 700 "$backup_root/full" "$backup_root/hourly-trading" "$backup_root/manifests"

case "$mode" in
  full)
    target="$backup_root/full/s78_market-$stamp.dump"
    pg_dump -Fc -Z9 -d "$database_name" -f "$target"
    keep=2
    directory="$backup_root/full"
    ;;
  trading)
    target="$backup_root/hourly-trading/trading-$stamp.dump"
    pg_dump -Fc -Z9 -d "$database_name" -t 'trading_*' -f "$target"
    keep=24
    directory="$backup_root/hourly-trading"
    ;;
  *)
    echo "Usage: $0 full|trading" >&2
    exit 2
    ;;
esac

chmod 600 "$target"
manifest="$backup_root/manifests/$(basename "${target%.dump}").sha256"
shasum -a 256 "$target" > "$manifest"
chmod 600 "$manifest"

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

echo "$mode backup ready: $target"
