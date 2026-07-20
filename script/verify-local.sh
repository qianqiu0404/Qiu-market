#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -f .env ]; then
  # shellcheck disable=SC1091
  source .env
fi

: "${MARKET_MASTER_DB_HOST:=127.0.0.1}"
: "${MARKET_MASTER_DB_PORT:=5432}"
: "${MARKET_MASTER_DB_USER:=xiuqiu}"
: "${MARKET_MASTER_DB_NAME:=s78_market}"
: "${MARKET_HTTP_HOST:=127.0.0.1}"
: "${MARKET_HTTP_PORT:=9092}"
: "${MARKET_REDIS_ADDRESS:=127.0.0.1:6379}"

echo "Checking PostgreSQL..."
pg_isready \
  -h "$MARKET_MASTER_DB_HOST" \
  -p "$MARKET_MASTER_DB_PORT" \
  -U "$MARKET_MASTER_DB_USER" \
  -d "$MARKET_MASTER_DB_NAME"

echo "Checking Redis..."
redis_host="${MARKET_REDIS_ADDRESS%:*}"
redis_port="${MARKET_REDIS_ADDRESS##*:}"
if [ -n "${MARKET_REDIS_PASSWORD:-}" ]; then
  redis-cli -h "$redis_host" -p "$redis_port" -a "$MARKET_REDIS_PASSWORD" ping
else
  redis-cli -h "$redis_host" -p "$redis_port" ping
fi

echo "Building backend..."
go build -o market-services ./cmd/market-services

echo "Starting API for verification..."
api_log="$(mktemp /tmp/s78-market-api.XXXXXX.log)"
./market-services api > "$api_log" 2>&1 &
api_pid=$!
cleanup() {
  kill "$api_pid" 2>/dev/null || true
  wait "$api_pid" 2>/dev/null || true
}
trap cleanup EXIT

base_url="http://${MARKET_HTTP_HOST}:${MARKET_HTTP_PORT}"
for _ in $(seq 1 20); do
  if curl -fsS "$base_url/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

echo "Checking API health..."
curl -fsS "$base_url/healthz" >/dev/null

echo "Checking dashboard API..."
dashboard_json="$(
  curl -fsS -X POST "$base_url/api/v1/get_market_dashboard" \
    -H 'Content-Type: application/json' \
    -d '{"page":1,"page_size":3}'
)"

if ! printf '%s' "$dashboard_json" | grep -q '"code":2000'; then
  echo "Dashboard API did not return success:"
  printf '%s\n' "$dashboard_json"
  echo "API log:"
  tail -n 30 "$api_log"
  exit 1
fi

echo "Local verification passed."
printf '%s\n' "$dashboard_json"
