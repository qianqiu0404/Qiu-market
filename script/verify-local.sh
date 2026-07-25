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

base_url="http://${MARKET_HTTP_HOST}:${MARKET_HTTP_PORT}"
api_log=""
api_pid=""
cleanup() {
  if [ -n "$api_pid" ]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  if [ -n "$api_log" ]; then
    rm -f "$api_log"
  fi
}
trap cleanup EXIT

if curl -fsS "$base_url/healthz" >/dev/null 2>&1; then
  echo "Using the API already listening at $base_url..."
else
  echo "Starting a temporary API for verification..."
  api_log="$(mktemp /tmp/s78-market-api.XXXXXX)"
  ./market-services api > "$api_log" 2>&1 &
  api_pid=$!
  for _ in $(seq 1 20); do
    if curl -fsS "$base_url/healthz" >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
fi

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
  if [ -n "$api_log" ]; then
    echo "API log:"
    tail -n 30 "$api_log"
  fi
  exit 1
fi

echo "Checking seven-provider selection dashboards..."
validate_asset_dashboard() {
  local venue="$1"
  local universe="$2"
  local expected_kind="$3"
  local first_response
  local second_response=""
  local total
  first_response="$(
    curl -fsS -X POST "$base_url/api/v2/get_asset_dashboard" \
      -H 'Content-Type: application/json' \
      -d "{\"page\":1,\"page_size\":100,\"venue\":\"$venue\",\"filter\":\"assets\",\"sort_by\":\"rank\",\"sort_direction\":\"asc\",\"include_uncovered\":true,\"universe\":\"$universe\"}"
  )"
  total="$(printf '%s' "$first_response" | node -e '
    const fs = require("node:fs")
    const payload = JSON.parse(fs.readFileSync(0, "utf8"))
    process.stdout.write(String(Number(payload.total) || 0))
  ')"
  if [ "$total" -gt 100 ]; then
    second_response="$(
      curl -fsS -X POST "$base_url/api/v2/get_asset_dashboard" \
        -H 'Content-Type: application/json' \
        -d "{\"page\":2,\"page_size\":100,\"venue\":\"$venue\",\"filter\":\"assets\",\"sort_by\":\"rank\",\"sort_direction\":\"asc\",\"include_uncovered\":true,\"universe\":\"$universe\"}"
    )"
  fi
  printf '%s\n%s\n' "$first_response" "$second_response" | node -e '
    const fs = require("node:fs")
    const venue = process.argv[1]
    const expectedKind = process.argv[2]
    const payloads = fs.readFileSync(0, "utf8").split("\n").filter(Boolean).map(JSON.parse)
    const payload = payloads[0]
    const rows = payloads.flatMap((page) => Array.isArray(page.result) ? page.result : [])
    const ids = rows.map((row) => String(row.asset_id || "")).filter(Boolean)
    const unique = new Set(ids)
    const total = Number(payload.total)
    if (payloads.some((page) => page.code !== 2000)) {
      throw new Error(`${venue}: one or more API pages failed`)
    }
    if (ids.length !== rows.length || unique.size !== rows.length) {
      throw new Error(`${venue}: empty or duplicate canonical asset_id`)
    }
    if (total !== rows.length) {
      throw new Error(`${venue}: total ${total} does not match page rows ${rows.length}`)
    }
    if (expectedKind === "provider" && total !== 50) {
      throw new Error(`${venue}: expected versioned 50-asset selection, got ${total}`)
    }
    if (expectedKind === "union" && total < 50) {
      throw new Error(`${venue}: canonical union unexpectedly contains only ${total} assets`)
    }
    console.log(`${venue}: ${total} rows, ${unique.size} unique canonical assets`)
  ' "$venue" "$expected_kind"
}

validate_asset_dashboard all provider_union union
for venue in binance coinbase bybit okx hyperliquid uniswap pancakeswap; do
  validate_asset_dashboard "$venue" provider_top50 provider
done

echo "Local verification passed."
