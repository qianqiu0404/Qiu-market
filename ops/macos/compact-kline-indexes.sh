#!/usr/bin/env bash
set -euo pipefail

database_name="${MARKET_DB_NAME:-s78_market}"

available_kib="$(df -k /System/Volumes/Data | awk 'NR == 2 { print $4 }')"
if [ -z "$available_kib" ] || [ "$available_kib" -lt $((25 * 1024 * 1024)) ]; then
  echo "K-line index compaction requires at least 25 GiB free." >&2
  exit 1
fi

psql -X -v ON_ERROR_STOP=1 -d "$database_name" \
  -c 'VACUUM (ANALYZE) symbol_kline'

for index_name in \
  symbol_kline_pkey \
  symbol_kline_business_key_uidx \
  symbol_kline_sync_seq_idx \
  symbol_kline_interval_open_time_idx
do
  echo "Reindexing $index_name concurrently..."
  psql -X -v ON_ERROR_STOP=1 -d "$database_name" \
    -c "REINDEX INDEX CONCURRENTLY $index_name"
done

psql -X -v ON_ERROR_STOP=1 -d "$database_name" -c "
SELECT
  pg_size_pretty(pg_total_relation_size('symbol_kline')) AS total,
  pg_size_pretty(pg_relation_size('symbol_kline')) AS heap,
  pg_size_pretty(pg_indexes_size('symbol_kline')) AS indexes"
