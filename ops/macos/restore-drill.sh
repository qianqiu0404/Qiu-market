#!/usr/bin/env bash
set -euo pipefail
umask 077

support_dir="/Users/xiuqiu/Library/Application Support/Qiu Market"
backup_root="$support_dir/backups"
latest="$(find "$backup_root/full" -maxdepth 1 -type f -name '*.dump' -print | sort -r | head -1)"
if [ -z "$latest" ]; then
  echo "No full Qiu Market backup is available for a restore drill." >&2
  exit 1
fi

manifest="$backup_root/manifests/$(basename "${latest%.dump}").sha256"
if [ ! -f "$manifest" ]; then
  echo "Backup manifest is missing: $manifest" >&2
  exit 1
fi
(cd "$(dirname "$latest")" && shasum -a 256 -c "$manifest")

drill_db="qiu_market_restore_drill_$(date '+%Y%m%d_%H%M%S')"
cleanup() {
  dropdb --if-exists "$drill_db" >/dev/null 2>&1 || true
}
trap cleanup EXIT

createdb "$drill_db"
pg_restore --no-owner --no-privileges -d "$drill_db" "$latest"

source_events="$(psql -X -At -d s78_market -c 'SELECT COALESCE(max(sequence),0) FROM trading_event_batch')"
restored_events="$(psql -X -At -d "$drill_db" -c 'SELECT COALESCE(max(sequence),0) FROM trading_event_batch')"
source_snapshot="$(psql -X -At -d s78_market -c 'SELECT COALESCE(max(sequence),0) FROM trading_snapshot')"
restored_snapshot="$(psql -X -At -d "$drill_db" -c 'SELECT COALESCE(max(sequence),0) FROM trading_snapshot')"
ledger_imbalances="$(psql -X -At -d "$drill_db" -c 'SELECT count(*) FROM (SELECT asset FROM trading_ledger_entry GROUP BY asset HAVING sum(amount) <> 0) AS imbalanced')"

if [ "$source_events" -lt "$restored_events" ] ||
  [ "$source_snapshot" -lt "$restored_snapshot" ] ||
  [ "$ledger_imbalances" != "0" ]; then
  echo "Restore drill validation failed." >&2
  exit 1
fi

echo "Restore drill passed: backup_events=$restored_events current_events=$source_events snapshot=$restored_snapshot ledger_imbalances=0"
