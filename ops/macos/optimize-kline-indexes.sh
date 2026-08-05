#!/usr/bin/env bash
set -euo pipefail

database_name="${MARKET_DB_NAME:-s78_market}"

available_kib="$(df -k /System/Volumes/Data | awk 'NR == 2 { print $4 }')"
if [ -z "$available_kib" ] || [ "$available_kib" -lt $((35 * 1024 * 1024)) ]; then
  echo "K-line index optimization requires at least 35 GiB free." >&2
  exit 1
fi

echo "Dropping redundant K-line indexes concurrently..."
for index_name in \
  idx_symbol_kline_guid \
  symbol_kline_market_interval_open_idx \
  idx_symbol_kline_symbol_interval_created_at \
  idx_symbol_kline_is_active \
  idx_symbol_kline_created_at
do
  psql -X -v ON_ERROR_STOP=1 -d "$database_name" \
    -c "DROP INDEX CONCURRENTLY IF EXISTS $index_name"
done

echo "Creating the bounded-retention index concurrently..."
psql -X -v ON_ERROR_STOP=1 -d "$database_name" \
  -c 'CREATE INDEX CONCURRENTLY IF NOT EXISTS symbol_kline_interval_open_time_idx ON symbol_kline ("interval", open_time)'

psql -X -v ON_ERROR_STOP=1 -d "$database_name" -c "
SELECT indexrelname, pg_size_pretty(pg_relation_size(indexrelid)) AS size
FROM pg_stat_user_indexes
WHERE relname = 'symbol_kline'
ORDER BY pg_relation_size(indexrelid) DESC"
