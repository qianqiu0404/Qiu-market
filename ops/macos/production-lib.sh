#!/usr/bin/env bash

# Shared production helpers. Callers are expected to enable `set -euo pipefail`
# before sourcing this file.

qiu_repo_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

qiu_load_production_environment() {
  local repo_root="$1"
  QIU_MARKET_SUPPORT_DIR="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
  QIU_MARKET_ENV_FILE="${QIU_MARKET_ENV_FILE:-$QIU_MARKET_SUPPORT_DIR/production.env}"

  set -a
  if [ -f "$repo_root/.env" ]; then
    # shellcheck disable=SC1091
    source "$repo_root/.env"
  fi
  if [ -f "$QIU_MARKET_ENV_FILE" ]; then
    # shellcheck disable=SC1090
    source "$QIU_MARKET_ENV_FILE"
  fi
  set +a

  QIU_MARKET_DB_NAME="${MARKET_MASTER_DB_NAME:-${MARKET_DB_NAME:-s78_market}}"
  QIU_MARKET_DB_HOST="${MARKET_MASTER_DB_HOST:-127.0.0.1}"
  QIU_MARKET_DB_PORT="${MARKET_MASTER_DB_PORT:-5432}"
  QIU_MARKET_DB_USER="${MARKET_MASTER_DB_USER:-$(id -un)}"
  export PGPASSWORD="${MARKET_MASTER_DB_PASSWORD:-}"
}

qiu_require_private_environment() {
  local permissions
  if [ ! -f "$QIU_MARKET_ENV_FILE" ]; then
    echo "Private production environment is missing: $QIU_MARKET_ENV_FILE" >&2
    return 1
  fi
  permissions="$(stat -f '%Lp' "$QIU_MARKET_ENV_FILE")"
  if [ "$permissions" != "600" ] && [ "$permissions" != "400" ]; then
    echo "Private production environment must have mode 0600 or 0400: $QIU_MARKET_ENV_FILE" >&2
    return 1
  fi
}

qiu_require_command() {
  local command_name="$1"
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Required command is unavailable: $command_name" >&2
    return 1
  fi
}

qiu_psql() {
  psql -X -v ON_ERROR_STOP=1 -At \
    --host="$QIU_MARKET_DB_HOST" \
    --port="$QIU_MARKET_DB_PORT" \
    --username="$QIU_MARKET_DB_USER" \
    --dbname="$QIU_MARKET_DB_NAME" \
    "$@"
}

qiu_outbox_integrity_stats() {
  local database_name="${1:-$QIU_MARKET_DB_NAME}"
  psql -X -v ON_ERROR_STOP=1 -At -F '|' \
    --host="$QIU_MARKET_DB_HOST" \
    --port="$QIU_MARKET_DB_PORT" \
    --username="$QIU_MARKET_DB_USER" \
    --dbname="$database_name" \
    -c "
WITH latest_outbox AS (
  SELECT DISTINCT ON (market_id)
    market_id, sequence, event_index
  FROM trading_outbox
  ORDER BY market_id, sequence DESC, event_index DESC
),
latest_feed AS (
  SELECT DISTINCT ON (market_id)
    market_id, sequence, event_index
  FROM trading_event_feed
  ORDER BY market_id, sequence DESC, event_index DESC
),
cursor_markets AS (
  SELECT market_id FROM latest_outbox
  UNION
  SELECT market_id FROM latest_feed
  UNION
  SELECT market_id FROM trading_outbox_checkpoint
),
cursor_mismatches AS (
  SELECT count(*) AS count
  FROM cursor_markets cursor_market
  LEFT JOIN latest_outbox source USING (market_id)
  LEFT JOIN latest_feed feed USING (market_id)
  LEFT JOIN trading_outbox_checkpoint checkpoint USING (market_id)
  WHERE feed.market_id IS NULL
     OR checkpoint.market_id IS NULL
     OR (feed.sequence, feed.event_index)
          IS DISTINCT FROM (checkpoint.sequence, checkpoint.event_index)
     OR (
       source.market_id IS NOT NULL
       AND (source.sequence, source.event_index)
            IS DISTINCT FROM (feed.sequence, feed.event_index)
     )
),
published_mismatches AS (
  SELECT count(*) AS count
  FROM trading_outbox source
  LEFT JOIN trading_event_feed feed
    ON feed.market_id=source.market_id
   AND feed.sequence=source.sequence
   AND feed.event_index=source.event_index
  WHERE source.published_at IS NOT NULL
    AND (
      feed.market_id IS NULL
      OR feed.event_type IS DISTINCT FROM source.event_type
      OR feed.payload IS DISTINCT FROM source.payload
    )
)
SELECT
  (SELECT count(*) FROM trading_outbox WHERE published_at IS NULL),
  COALESCE((
    SELECT floor(extract(epoch FROM clock_timestamp() - min(created_at)))::bigint
    FROM trading_outbox
    WHERE published_at IS NULL
  ), 0),
  (SELECT count FROM published_mismatches),
  (SELECT count FROM cursor_mismatches),
  (SELECT count(*) FROM trading_event_feed),
  (SELECT count(*) FROM trading_outbox)
"
}

qiu_sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

qiu_launch_domain() {
  local role="${1:-api}"
  if launchctl print "system/com.qiumarket.$role" >/dev/null 2>&1; then
    printf '%s\n' "system"
  else
    printf 'gui/%s\n' "$(id -u)"
  fi
}

qiu_restart_role() {
  local role="$1"
  local domain
  domain="$(qiu_launch_domain "$role")"
  launchctl kickstart -k "$domain/com.qiumarket.$role"
}

qiu_signed_trading_status() {
  local request_path="/api/v1/trading/markets/BTC-USDT/status"
  local secret="${MARKET_PUBLIC_PROXY_HMAC_SECRET:-}"
  if [ -z "$secret" ]; then
    curl --fail --silent --show-error --max-time 5 \
      "http://127.0.0.1:9092$request_path"
    return
  fi

  local timestamp
  local digest
  local canonical
  local signature
  timestamp="$(date '+%s')"
  digest="e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
  canonical="$(printf '%s\nGET\n%s\n%s' "$timestamp" "$request_path" "$digest")"
  signature="$(
    printf '%s' "$canonical" |
      /usr/bin/openssl dgst -sha256 -hmac "$secret" -binary |
      /usr/bin/xxd -p -c 256
  )"
  curl --fail --silent --show-error --max-time 5 \
    --header "X-Qiu-Market-Timestamp: $timestamp" \
    --header "X-Qiu-Market-Content-SHA256: $digest" \
    --header "X-Qiu-Market-Signature: $signature" \
    "http://127.0.0.1:9092$request_path"
}

qiu_wait_for_api() {
  local attempts="${1:-60}"
  local attempt
  for attempt in $(seq 1 "$attempts"); do
    if curl --fail --silent --max-time 3 \
      http://127.0.0.1:9092/healthz >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "Qiu Market API did not become healthy after $attempts attempts." >&2
  return 1
}

qiu_wait_for_trading_status() {
  local require_outbox="${1:-true}"
  local attempts="${2:-120}"
  local attempt
  local payload
  local state
  local last_error
  local outbox_state
  local outbox_error
  for attempt in $(seq 1 "$attempts"); do
    payload="$(qiu_signed_trading_status 2>/dev/null || true)"
    state="$(jq -r '.state // ""' <<<"$payload" 2>/dev/null || true)"
    last_error="$(jq -r '.last_error // ""' <<<"$payload" 2>/dev/null || true)"
    outbox_state="$(jq -r '.outbox_state // ""' <<<"$payload" 2>/dev/null || true)"
    outbox_error="$(jq -r '.outbox_last_error // ""' <<<"$payload" 2>/dev/null || true)"
    if [ "$state" = "ready" ] && [ -z "$last_error" ]; then
      if [ "$require_outbox" != "true" ] ||
        { [ "$outbox_state" = "ready" ] && [ -z "$outbox_error" ]; }; then
        printf '%s\n' "$payload"
        return 0
      fi
    fi
    sleep 1
  done
  echo "Trading did not reach the required ready state after $attempts attempts." >&2
  if [ -n "${payload:-}" ]; then
    jq '{state,last_error,outbox_state,outbox_last_error,sequence}' \
      <<<"$payload" >&2 2>/dev/null || true
  fi
  return 1
}
