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
  QIU_MARKET_DATABASE_ENV_FILE="${QIU_MARKET_DATABASE_ENV_FILE:-$QIU_MARKET_SUPPORT_DIR/database.env}"

  set -a
  if [ -n "${QIU_MARKET_DATABASE_ENV_FILE:-}" ] &&
    [ -f "$QIU_MARKET_DATABASE_ENV_FILE" ]; then
    # shellcheck disable=SC1090
    source "$QIU_MARKET_DATABASE_ENV_FILE"
  elif [ -f "$repo_root/.env" ]; then
    # shellcheck disable=SC1091
    source "$repo_root/.env"
  fi
  if [ -f "$QIU_MARKET_ENV_FILE" ]; then
    # shellcheck disable=SC1090
    source "$QIU_MARKET_ENV_FILE"
  fi
  set +a

  if [ -z "${MARKET_MIGRATIONS_DIR:-}" ] && [ -d "$repo_root/migrations" ]; then
    MARKET_MIGRATIONS_DIR="$repo_root/migrations"
    export MARKET_MIGRATIONS_DIR
  fi

  QIU_MARKET_DB_NAME="${MARKET_MASTER_DB_NAME:-${MARKET_DB_NAME:-s78_market}}"
  QIU_MARKET_DB_HOST="${MARKET_MASTER_DB_HOST:-127.0.0.1}"
  QIU_MARKET_DB_PORT="${MARKET_MASTER_DB_PORT:-5432}"
  QIU_MARKET_DB_USER="${MARKET_MASTER_DB_USER:-$(id -un)}"
  export PGPASSWORD="${MARKET_MASTER_DB_PASSWORD:-}"
}

qiu_require_private_environment() {
  local permissions
  local database_permissions
  local database_env_file="${QIU_MARKET_DATABASE_ENV_FILE:-}"
  if [ ! -f "$QIU_MARKET_ENV_FILE" ]; then
    echo "Private production environment is missing: $QIU_MARKET_ENV_FILE" >&2
    return 1
  fi
  permissions="$(stat -f '%Lp' "$QIU_MARKET_ENV_FILE")"
  if [ "$permissions" != "600" ] && [ "$permissions" != "400" ]; then
    echo "Private production environment must have mode 0600 or 0400: $QIU_MARKET_ENV_FILE" >&2
    return 1
  fi
  if [ -n "$database_env_file" ] && [ -f "$database_env_file" ]; then
    database_permissions="$(stat -f '%Lp' "$database_env_file")"
    if [ "$database_permissions" != "600" ] && [ "$database_permissions" != "400" ]; then
      echo "Private database environment must have mode 0600 or 0400: $database_env_file" >&2
      return 1
    fi
  fi
}

qiu_require_command() {
  local command_name="$1"
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Required command is unavailable: $command_name" >&2
    return 1
  fi
}

qiu_validate_cursor_hmac_value() {
  local value="$1"
  local key_id
  local encoded_secret
  local padded_secret
  local remainder
  local decoded_bytes

  if [[ "$value" != *:* ]] || [[ "${value#*:}" == *:* ]]; then
    echo "Cursor HMAC key must use key_id:unpadded-base64url-secret." >&2
    return 1
  fi
  key_id="${value%%:*}"
  encoded_secret="${value#*:}"
  if [[ ! "$key_id" =~ ^[A-Za-z0-9._-]{1,32}$ ]] ||
    [[ ! "$encoded_secret" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "Cursor HMAC key ID or unpadded base64url secret is invalid." >&2
    return 1
  fi
  remainder=$((${#encoded_secret} % 4))
  if [ "$remainder" -eq 1 ]; then
    echo "Cursor HMAC secret has an invalid base64url length." >&2
    return 1
  fi
  padded_secret="$encoded_secret"
  if [ "$remainder" -eq 2 ]; then
    padded_secret="${padded_secret}=="
  elif [ "$remainder" -eq 3 ]; then
    padded_secret="${padded_secret}="
  fi
  if ! decoded_bytes="$(
    printf '%s' "$padded_secret" |
      tr '_-' '/+' |
      base64 -D 2>/dev/null |
      wc -c |
      tr -d ' '
  )"; then
    echo "Cursor HMAC secret is not valid base64url." >&2
    return 1
  fi
  if [[ ! "$decoded_bytes" =~ ^[0-9]+$ ]] || [ "$decoded_bytes" -lt 32 ]; then
    echo "Cursor HMAC secret must decode to at least 32 bytes." >&2
    return 1
  fi
  printf '%s\n' "$key_id"
}

qiu_require_release_coordination() {
  local expected_operation="$1"
  local expected_subject="$2"
  local support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
  local expected_context="$support_dir/release-state/candidate-activation.lock/context.env"
  local context="${QIU_MARKET_COORDINATOR_CONTEXT:-}"
  local token="${QIU_MARKET_COORDINATOR_TOKEN:-}"
  local permissions
  local owner_uid
  local schema_version
  local operation
  local subject
  local coordinator_pid
  local expires_epoch
  local recorded_nonce
  local now

  if [ "$context" != "$expected_context" ] || [ ! -f "$context" ]; then
    echo "A live release coordinator context is required for $expected_operation." >&2
    return 1
  fi
  permissions="$(stat -f '%Lp' "$context")"
  owner_uid="$(stat -f '%u' "$context")"
  if { [ "$permissions" != 600 ] && [ "$permissions" != 400 ]; } ||
    [ "$owner_uid" != "$(id -u)" ]; then
    echo "Release coordinator context has unsafe ownership or permissions." >&2
    return 1
  fi
  if [[ ! "$token" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Release coordinator token is invalid." >&2
    return 1
  fi
  schema_version="$(sed -n 's/^schema_version=//p' "$context" | head -1)"
  operation="$(sed -n 's/^operation=//p' "$context" | head -1)"
  subject="$(sed -n 's/^subject=//p' "$context" | head -1)"
  coordinator_pid="$(sed -n 's/^coordinator_pid=//p' "$context" | head -1)"
  expires_epoch="$(sed -n 's/^expires_epoch=//p' "$context" | head -1)"
  recorded_nonce="$(sed -n 's/^nonce=//p' "$context" | head -1)"
  now="$(date '+%s')"
  if [ "$schema_version" != 1 ] ||
    [ "$operation" != "$expected_operation" ] ||
    [ "$subject" != "$expected_subject" ] ||
    [[ ! "$coordinator_pid" =~ ^[0-9]+$ ]] ||
    [ "$coordinator_pid" != "$PPID" ] ||
    ! kill -0 "$coordinator_pid" >/dev/null 2>&1 ||
    [[ ! "$expires_epoch" =~ ^[0-9]+$ ]] ||
    [ "$expires_epoch" -lt "$now" ] ||
    [ "$expires_epoch" -gt $((now + 300)) ] ||
    [ "$recorded_nonce" != "$token" ]; then
    echo "Release coordinator context does not match this exact operation." >&2
    return 1
  fi

  # One context authorizes one child action only. Consume it before any
  # production mutation so retries must return through the coordinator.
  find "$context" -maxdepth 0 -type f -delete
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
