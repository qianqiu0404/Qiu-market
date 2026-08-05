#!/usr/bin/env bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "$repo_root/ops/macos/production-lib.sh"
qiu_load_production_environment "$repo_root"
qiu_require_private_environment

support_dir="$QIU_MARKET_SUPPORT_DIR"
backup_root="$support_dir/backups"
binary="${QIU_MARKET_BINARY:-$support_dir/bin/market-services}"

database_name="$QIU_MARKET_DB_NAME"
database_host="$QIU_MARKET_DB_HOST"
database_port="$QIU_MARKET_DB_PORT"
database_user="$QIU_MARKET_DB_USER"

if [ ! -x "$binary" ]; then
  echo "Qiu Market managed binary is unavailable: $binary" >&2
  exit 1
fi

latest="${QIU_MARKET_BACKUP:-}"
if [ -z "$latest" ]; then
  latest="$(find "$backup_root/full" -maxdepth 1 -type f -name '*.dump' -print | sort -r | head -1)"
fi
if [ -z "$latest" ]; then
  echo "No full Qiu Market backup is available for a restore drill." >&2
  exit 1
fi
if [ ! -f "$latest" ]; then
  echo "Requested Qiu Market backup is unavailable: $latest" >&2
  exit 1
fi

manifest="$backup_root/manifests/$(basename "${latest%.dump}").sha256"
if [ ! -f "$manifest" ]; then
  echo "Backup manifest is missing: $manifest" >&2
  exit 1
fi
(cd "$(dirname "$latest")" && shasum -a 256 -c "$manifest")

drill_db="qiu_market_restore_drill_$(date '+%Y%m%d_%H%M%S')"
drill_log="$(mktemp /tmp/qiu-market-restore-drill.XXXXXX)"
verifier_pid=""
cleanup() {
  if [ -n "$verifier_pid" ] && kill -0 "$verifier_pid" >/dev/null 2>&1; then
    kill -TERM "$verifier_pid" >/dev/null 2>&1 || true
    wait "$verifier_pid" >/dev/null 2>&1 || true
  fi
  dropdb \
    --host="$database_host" \
    --port="$database_port" \
    --username="$database_user" \
    --if-exists \
    "$drill_db" >/dev/null 2>&1 || true
  find "$drill_log" -maxdepth 0 -type f -delete 2>/dev/null || true
}
trap cleanup EXIT

createdb \
  --host="$database_host" \
  --port="$database_port" \
  --username="$database_user" \
  "$drill_db"
pg_restore \
  --host="$database_host" \
  --port="$database_port" \
  --username="$database_user" \
  --no-owner \
  --no-privileges \
  --dbname="$drill_db" \
  "$latest"

psql_base=(
  psql -X -v ON_ERROR_STOP=1 -At
  --host="$database_host"
  --port="$database_port"
  --username="$database_user"
)
source_events="$("${psql_base[@]}" -d "$database_name" -c 'SELECT COALESCE(max(sequence),0) FROM trading_event_batch')"
restored_events="$("${psql_base[@]}" -d "$drill_db" -c 'SELECT COALESCE(max(sequence),0) FROM trading_event_batch')"
source_snapshot="$("${psql_base[@]}" -d "$database_name" -c 'SELECT COALESCE(max(sequence),0) FROM trading_snapshot')"
restored_snapshot="$("${psql_base[@]}" -d "$drill_db" -c 'SELECT COALESCE(max(sequence),0) FROM trading_snapshot')"
ledger_imbalances="$("${psql_base[@]}" -d "$drill_db" -c 'SELECT count(*) FROM (SELECT asset FROM trading_ledger_entry GROUP BY asset HAVING sum(amount) <> 0) AS imbalanced')"
event_gaps="$("${psql_base[@]}" -d "$drill_db" -c "
SELECT count(*)
FROM (
  SELECT market_id, sequence,
         lag(sequence) OVER (PARTITION BY market_id ORDER BY sequence) AS previous
  FROM trading_event_batch
) ordered
WHERE previous IS NOT NULL AND sequence <> previous + 1
")"
snapshot_hash_mismatches="$("${psql_base[@]}" -d "$drill_db" -c "
SELECT count(*)
FROM trading_snapshot snapshot
LEFT JOIN trading_event_batch event
  ON event.market_id=snapshot.market_id
 AND event.sequence=snapshot.sequence
WHERE snapshot.sequence > 0
  AND event.state_hash IS DISTINCT FROM snapshot.state_hash
")"

if [ "$source_events" -lt "$restored_events" ] ||
  [ "$source_snapshot" -lt "$restored_snapshot" ] ||
  [ "$ledger_imbalances" != "0" ] ||
  [ "$event_gaps" != "0" ] ||
  [ "$snapshot_hash_mismatches" != "0" ]; then
  echo "Restore drill validation failed." >&2
  exit 1
fi

if ! (
  cd "$repo_root"
  export MARKET_MASTER_DB_NAME="$drill_db"
  "$binary" migrate
) >"$drill_log" 2>&1; then
  echo "Current migrations failed against the restored temporary database." >&2
  tail -n 20 "$drill_log" >&2
  exit 1
fi

(
  cd "$repo_root"
  export MARKET_MASTER_DB_NAME="$drill_db"
  export MARKET_TRADING_GRPC_ADDR="127.0.0.1:0"
  export MARKET_TRADING_DEMO_MAKER_ENABLED=false
  exec "$binary" trading
) >"$drill_log" 2>&1 &
verifier_pid="$!"

recovery_ready=false
for _ in $(seq 1 80); do
  if ! kill -0 "$verifier_pid" >/dev/null 2>&1; then
    break
  fi
  if grep -q "virtual trading backend ready" "$drill_log"; then
    recovery_ready=true
    break
  fi
  sleep 0.25
done
if [ "$recovery_ready" != true ]; then
  echo "Restored trading process did not reach ready state." >&2
  tail -n 20 "$drill_log" >&2
  exit 1
fi

outbox_ready=false
outbox_stats=""
for _ in $(seq 1 240); do
  outbox_stats="$(qiu_outbox_integrity_stats "$drill_db" 2>/dev/null || true)"
  if [ -n "$outbox_stats" ]; then
    IFS='|' read -r \
      outbox_unpublished \
      outbox_oldest_seconds \
      outbox_payload_mismatches \
      outbox_cursor_mismatches \
      outbox_feed_rows \
      outbox_source_rows \
      <<<"$outbox_stats"
    if [ "$outbox_unpublished" -eq 0 ] &&
      [ "$outbox_oldest_seconds" -eq 0 ] &&
      [ "$outbox_payload_mismatches" -eq 0 ] &&
      [ "$outbox_cursor_mismatches" -eq 0 ] &&
      { [ "$outbox_source_rows" -eq 0 ] || [ "$outbox_feed_rows" -gt 0 ]; }; then
      outbox_ready=true
      break
    fi
  fi
  sleep 1
done
if [ "$outbox_ready" != true ]; then
  echo "Restored outbox/feed/checkpoint did not converge: ${outbox_stats:-unavailable}" >&2
  tail -n 20 "$drill_log" >&2
  exit 1
fi

kill -TERM "$verifier_pid"
if ! wait "$verifier_pid"; then
  echo "Restored trading process did not stop cleanly." >&2
  tail -n 20 "$drill_log" >&2
  exit 1
fi
verifier_pid=""

recovered_proof="$("${psql_base[@]}" -d "$drill_db" -F '|' -c "
SELECT market.current_sequence,
       snapshot.sequence,
       CASE
         WHEN snapshot.sequence = 0 THEN TRUE
         ELSE snapshot.state_hash = event.state_hash
       END AS state_hash_matches
FROM trading_market market
JOIN trading_snapshot snapshot USING (market_id)
LEFT JOIN trading_event_batch event
  ON event.market_id=snapshot.market_id
 AND event.sequence=snapshot.sequence
")"
IFS='|' read -r recovered_sequence recovered_snapshot recovered_hash_matches \
  <<<"$recovered_proof"
if [ "$recovered_sequence" != "$restored_events" ] ||
  [ "$recovered_snapshot" != "$restored_events" ] ||
  [ "$recovered_hash_matches" != "t" ]; then
  echo "Restored trading state hash or sequence diverged." >&2
  exit 1
fi

echo "Restore drill passed: backup_events=$restored_events current_events=$source_events recovered_sequence=$recovered_sequence state_hash_matches=true ledger_imbalances=0 outbox_unpublished=0 outbox_payload_mismatches=0 outbox_cursor_mismatches=0"
