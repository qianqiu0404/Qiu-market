#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -f .env ]; then
  # shellcheck disable=SC1091
  source .env
fi
if [ -n "${MARKET_RUNTIME_ENV_FILE:-}" ]; then
  if [ ! -f "$MARKET_RUNTIME_ENV_FILE" ]; then
    echo "MARKET_RUNTIME_ENV_FILE does not exist: $MARKET_RUNTIME_ENV_FILE" >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  source "$MARKET_RUNTIME_ENV_FILE"
fi

: "${MARKET_MASTER_DB_HOST:=127.0.0.1}"
: "${MARKET_MASTER_DB_PORT:=5432}"
: "${MARKET_MASTER_DB_USER:=xiuqiu}"
: "${MARKET_MASTER_DB_NAME:=s78_market}"
: "${MARKET_HTTP_HOST:=127.0.0.1}"
: "${MARKET_HTTP_PORT:=9092}"
: "${MARKET_TRADING_GRPC_ADDR:=127.0.0.1:9094}"
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
api_request() {
  local method="$1"
  local request_uri="$2"
  local request_body="${3:-}"
  local timestamp
  local digest
  local canonical
  local signature
  local -a headers=()

  if [ -n "${MARKET_PUBLIC_PROXY_HMAC_SECRET:-}" ]; then
    timestamp="$(date +%s)"
    digest="$(printf '%s' "$request_body" | shasum -a 256 | awk '{print $1}')"
    canonical="$(printf '%s\n%s\n%s\n%s' "$timestamp" "$method" "$request_uri" "$digest")"
    signature="$(
      printf '%s' "$canonical" |
        openssl dgst -sha256 -hmac "$MARKET_PUBLIC_PROXY_HMAC_SECRET" -hex |
        awk '{print $NF}'
    )"
    headers+=(
      -H "X-Qiu-Market-Timestamp: $timestamp"
      -H "X-Qiu-Market-Content-SHA256: $digest"
      -H "X-Qiu-Market-Signature: $signature"
    )
  fi
  if [ -n "$request_body" ]; then
    headers+=(-H "Content-Type: application/json" --data-binary "$request_body")
  fi
  curl -fsS -X "$method" "${headers[@]}" "$base_url$request_uri"
}

api_log=""
api_pid=""
trading_log=""
trading_pid=""
cleanup() {
  if [ -n "$api_pid" ]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  if [ -n "$trading_pid" ]; then
    kill "$trading_pid" 2>/dev/null || true
    wait "$trading_pid" 2>/dev/null || true
  fi
  if [ -n "$api_log" ]; then
    rm -f "$api_log"
  fi
  if [ -n "$trading_log" ]; then
    rm -f "$trading_log"
  fi
}
trap cleanup EXIT

echo "Checking canonical trading migration..."
export PGPASSWORD="${MARKET_MASTER_DB_PASSWORD:-}"
trading_schema_ready="$(
  psql \
    -h "$MARKET_MASTER_DB_HOST" \
    -p "$MARKET_MASTER_DB_PORT" \
    -U "$MARKET_MASTER_DB_USER" \
    -d "$MARKET_MASTER_DB_NAME" \
    -Atqc "SELECT to_regclass('public.trading_market') IS NOT NULL
      AND to_regclass('public.trading_event_batch') IS NOT NULL
      AND to_regclass('public.trading_user_session') IS NOT NULL"
)"
if [ "$trading_schema_ready" != "t" ]; then
  echo "Trading schema is missing; run the backed-up migration workflow before verification." >&2
  exit 1
fi

trading_port="${MARKET_TRADING_GRPC_ADDR##*:}"
case "$trading_port" in
  ''|*[!0-9]*)
    echo "MARKET_TRADING_GRPC_ADDR must end in a numeric port." >&2
    exit 1
    ;;
esac
if lsof -nP -iTCP:"$trading_port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Using the trading service already listening at $MARKET_TRADING_GRPC_ADDR..."
else
  echo "Starting a temporary virtual trading service with demo-maker disabled..."
  trading_log="$(mktemp /tmp/s78-market-trading.XXXXXX)"
  MARKET_TRADING_DEMO_MAKER_ENABLED=false \
    ./market-services trading > "$trading_log" 2>&1 &
  trading_pid=$!
  for _ in $(seq 1 30); do
    if lsof -nP -iTCP:"$trading_port" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    if ! kill -0 "$trading_pid" 2>/dev/null; then
      echo "Temporary trading service exited during startup:" >&2
      tail -n 30 "$trading_log" >&2
      exit 1
    fi
    sleep 0.25
  done
  if ! lsof -nP -iTCP:"$trading_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Temporary trading service did not listen on $MARKET_TRADING_GRPC_ADDR:" >&2
    tail -n 30 "$trading_log" >&2
    exit 1
  fi
fi

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

echo "Checking isolated trading gateway..."
trading_status=""
for _ in $(seq 1 20); do
  if trading_status="$(
    api_request GET "/api/v1/trading/markets/BTC-USDT/status" 2>/dev/null
  )"; then
    break
  fi
  trading_status=""
  sleep 0.25
done
if [ -z "$trading_status" ]; then
  echo "Trading gateway did not become ready; restart a stale API if one was reused." >&2
  if [ -n "$api_log" ]; then
    tail -n 30 "$api_log" >&2
  fi
  exit 1
fi
printf '%s' "$trading_status" | node -e '
  const fs = require("node:fs")
  const payload = JSON.parse(fs.readFileSync(0, "utf8"))
  if (payload.market_id !== "BTC-USDT" || payload.state !== "ready") {
    throw new Error(`unexpected trading status: ${JSON.stringify(payload)}`)
  }
  if (!/^[0-9]+$/.test(String(payload.sequence))) {
    throw new Error("trading sequence must be an exact decimal string")
  }
  console.log(`trading: ${payload.state}, sequence ${payload.sequence}`)
'
api_request GET "/api/v1/trading/markets/BTC-USDT/orderbook?levels=5" \
  | node -e '
    const fs = require("node:fs")
    const payload = JSON.parse(fs.readFileSync(0, "utf8"))
    if (!Array.isArray(payload.bids) || !Array.isArray(payload.asks)) {
      throw new Error("trading order book must expose explicit bid/ask arrays")
    }
  '

echo "Checking dashboard API..."
dashboard_json="$(
  api_request POST "/api/v1/get_market_dashboard" '{"page":1,"page_size":3}'
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
    api_request POST "/api/v2/get_asset_dashboard" \
      "{\"page\":1,\"page_size\":100,\"venue\":\"$venue\",\"filter\":\"assets\",\"sort_by\":\"rank\",\"sort_direction\":\"asc\",\"include_uncovered\":true,\"universe\":\"$universe\"}"
  )"
  total="$(printf '%s' "$first_response" | node -e '
    const fs = require("node:fs")
    const payload = JSON.parse(fs.readFileSync(0, "utf8"))
    process.stdout.write(String(Number(payload.total) || 0))
  ')"
  if [ "$total" -gt 100 ]; then
    second_response="$(
      api_request POST "/api/v2/get_asset_dashboard" \
        "{\"page\":2,\"page_size\":100,\"venue\":\"$venue\",\"filter\":\"assets\",\"sort_by\":\"rank\",\"sort_direction\":\"asc\",\"include_uncovered\":true,\"universe\":\"$universe\"}"
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
