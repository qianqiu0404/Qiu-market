#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="${QIU_MARKET_SUPPORT_DIR:-/Users/xiuqiu/Library/Application Support/Qiu Market}"
state_dir="$support_dir/guardian"
log_dir="$support_dir/logs"
incident_log="$state_dir/incidents.log"
production_env="${QIU_MARKET_ENV_FILE:-$support_dir/production.env}"
database_env="${QIU_MARKET_DATABASE_ENV_FILE:-$support_dir/database.env}"

# shellcheck disable=SC1091
source "$repo_root/ops/macos/proxy-env.sh"
qiu_export_system_proxy

install -d -m 700 "$state_dir" "$log_dir"
touch "$incident_log"
chmod 600 "$incident_log"

if [ -f "$database_env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$database_env"
  set +a
fi
if [ -f "$production_env" ]; then
  set -a
  # shellcheck disable=SC1090
  source "$production_env"
  set +a
fi

if launchctl print system/com.qiumarket.api >/dev/null 2>&1; then
  launch_domain="system"
  launch_plist_dir="/Library/LaunchDaemons"
else
  launch_domain="gui/$(id -u xiuqiu)"
  launch_plist_dir="/Users/xiuqiu/Library/LaunchAgents"
fi

rotate_log() {
  local log_file="$1"
  local maximum_bytes="$2"
  local keep_bytes="$3"
  [ -f "$log_file" ] || return
  local size
  size="$(stat -f '%z' "$log_file")"
  if [ "$size" -gt "$maximum_bytes" ]; then
    cp "$log_file" "$log_file.1"
    tail -c "$keep_bytes" "$log_file.1" > "$log_file"
    chmod 600 "$log_file" "$log_file.1"
  fi
}

rotate_log "$incident_log" $((5 * 1024 * 1024)) $((1024 * 1024))

record() {
  printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >> "$incident_log"
}

record_throttled() {
  local key="$1"
  local cooldown="$2"
  shift 2
  local marker="$state_dir/$key-recorded-at"
  local now
  local previous
  now="$(date '+%s')"
  previous="$(cat "$marker" 2>/dev/null || echo 0)"
  if [ "$((now - previous))" -ge "$cooldown" ]; then
    printf '%s\n' "$now" > "$marker"
    record "$*"
  fi
}

api_healthy=false
if curl --fail --silent --max-time 3 http://127.0.0.1:9092/healthz >/dev/null; then
  api_healthy=true
fi

counter_file="$state_dir/api-failures"
api_failures="$(cat "$counter_file" 2>/dev/null || echo 0)"
if [ "$api_healthy" = true ]; then
  echo 0 > "$counter_file"
else
  api_failures=$((api_failures + 1))
  echo "$api_failures" > "$counter_file"
  if [ "$api_failures" -eq 3 ]; then
    record "api failed three consecutive health checks; kickstarting only API"
    launchctl kickstart -k "$launch_domain/com.qiumarket.api" || true
  fi
fi

signed_trading_status() {
  local request_path="/api/v1/trading/markets/BTC-USDT/status"
  local secret="${MARKET_PUBLIC_PROXY_HMAC_SECRET:-}"
  if [ -z "$secret" ]; then
    curl --fail --silent --max-time 5 "http://127.0.0.1:9092$request_path"
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
  curl --fail --silent --max-time 5 \
    --header "X-Qiu-Market-Timestamp: $timestamp" \
    --header "X-Qiu-Market-Content-SHA256: $digest" \
    --header "X-Qiu-Market-Signature: $signature" \
    "http://127.0.0.1:9092$request_path"
}

trading_healthy=false
trading_health_known=true
trading_state="unavailable"
if nc -z 127.0.0.1 9094 >/dev/null 2>&1; then
  if [ "$api_healthy" != true ]; then
    # Do not restart a healthy TCP listener merely because the API adapter is
    # unavailable. The API guardian owns that incident.
    trading_health_known=false
    trading_state="status-unobservable"
  else
    status_payload="$(signed_trading_status 2>/dev/null || true)"
    trading_state="$(jq -r '.state // "unavailable"' <<<"$status_payload" 2>/dev/null || echo unavailable)"
    if [ -z "$trading_state" ]; then
      trading_state="unavailable"
    fi
    trading_last_error="$(jq -r '.last_error // ""' <<<"$status_payload" 2>/dev/null || true)"
    trading_outbox_state="$(jq -r '.outbox_state // ""' <<<"$status_payload" 2>/dev/null || true)"
    trading_outbox_error="$(jq -r '.outbox_last_error // ""' <<<"$status_payload" 2>/dev/null || true)"
    if [ "$trading_state" = ready ] &&
      [ -z "$trading_last_error" ] &&
      { [ -z "$trading_outbox_state" ] || [ "$trading_outbox_state" = ready ]; } &&
      [ -z "$trading_outbox_error" ]; then
      trading_healthy=true
    fi
  fi
fi

trading_restart="$state_dir/trading-restart-at"
trading_stable_since="$state_dir/trading-stable-since"
trading_failures_file="$state_dir/trading-failures"
trading_failures="$(cat "$trading_failures_file" 2>/dev/null || echo 0)"
if [ "$trading_health_known" != true ]; then
  record_throttled \
    "trading-status-unobservable" \
    900 \
    "trading TCP listener is present but API status is unavailable; deferring restart to avoid a false positive"
elif [ "$trading_healthy" = true ]; then
  echo 0 > "$trading_failures_file"
  if [ ! -f "$trading_stable_since" ]; then
    date '+%s' > "$trading_stable_since"
  fi
  stable_since="$(cat "$trading_stable_since")"
  if [ -f "$trading_restart" ] &&
    [ "$(($(date '+%s') - stable_since))" -ge 900 ]; then
    find "$trading_restart" -maxdepth 0 -type f -delete
    record "trading remained ready for 15 minutes; automatic restart budget reset"
  fi
else
  find "$trading_stable_since" -maxdepth 0 -type f -delete 2>/dev/null || true
  trading_failures=$((trading_failures + 1))
  echo "$trading_failures" > "$trading_failures_file"
  if [ "$trading_failures" -ge 3 ] && [ ! -f "$trading_restart" ]; then
    date '+%s' > "$trading_restart"
    record "trading state=$trading_state failed three checks; performing its single automatic restart"
    launchctl kickstart -k "$launch_domain/com.qiumarket.trading" || true
  elif [ -f "$trading_restart" ]; then
    restarted_at="$(cat "$trading_restart")"
    if [ "$(($(date '+%s') - restarted_at))" -ge 120 ]; then
      record_throttled \
        "trading-still-unavailable" \
        900 \
        "trading state=$trading_state remains unavailable after its restart; leaving it offline for inspection"
    fi
  fi
fi

if ! pg_isready -q \
  -h "${MARKET_MASTER_DB_HOST:-127.0.0.1}" \
  -p "${MARKET_MASTER_DB_PORT:-5432}" \
  -d "${MARKET_MASTER_DB_NAME:-s78_market}"; then
  record_throttled \
    "postgres-unavailable" \
    900 \
    "PostgreSQL unavailable; guardian will not blindly restart the shared database"
fi

funnel_origin="${MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"
tailscale_socket="$support_dir/tailscale/tailscaled.sock"
tailscale_cli="/opt/homebrew/bin/tailscale"
funnel_restart="$state_dir/funnel-restart-at"
funnel_stable_since="$state_dir/funnel-stable-since"
funnel_failures_file="$state_dir/funnel-failures"
funnel_health_error="$state_dir/funnel-health-error"
funnel_failures="$(cat "$funnel_failures_file" 2>/dev/null || echo 0)"
if curl --fail --silent --show-error --max-time 8 \
  "$funnel_origin/healthz" >/dev/null 2>"$funnel_health_error"; then
  echo 0 > "$funnel_failures_file"
  : > "$funnel_health_error"
  if [ ! -f "$funnel_stable_since" ]; then
    date '+%s' > "$funnel_stable_since"
  fi
  funnel_stable_at="$(cat "$funnel_stable_since")"
  if [ -f "$funnel_restart" ] &&
    [ "$(($(date '+%s') - funnel_stable_at))" -ge 900 ]; then
    find "$funnel_restart" -maxdepth 0 -type f -delete
    record "Funnel remained healthy for 15 minutes; automatic restart budget reset"
  fi
elif [ "$api_healthy" = true ]; then
  find "$funnel_stable_since" -maxdepth 0 -type f -delete 2>/dev/null || true
  funnel_failures=$((funnel_failures + 1))
  echo "$funnel_failures" > "$funnel_failures_file"
  if [ "$funnel_failures" -ge 3 ] &&
    [ ! -f "$funnel_restart" ]; then
    date '+%s' > "$funnel_restart"
    echo 0 > "$funnel_failures_file"
    record "Funnel failed three checks while local API is healthy; using its single automatic tailscaled restart"
    launchctl kickstart -k "$launch_domain/com.qiumarket.tailscaled" || true
    tailscale_state="NoState"
    for _ in $(seq 1 20); do
      tailscale_state="$(
        "$tailscale_cli" --socket="$tailscale_socket" status --json 2>/dev/null |
          jq -r '.BackendState // "NoState"' 2>/dev/null || echo NoState
      )"
      if [ "$tailscale_state" = Running ]; then
        break
      fi
      sleep 1
    done
    if [ "$tailscale_state" = Running ]; then
      if ! "$tailscale_cli" --socket="$tailscale_socket" \
        funnel --bg --yes --https=443 http://127.0.0.1:9092; then
        record_throttled \
          "funnel-reconfigure-failed" \
          900 \
          "tailscaled restarted but Funnel reconfiguration failed; restart budget remains consumed"
      fi
    else
      record_throttled \
        "tailscale-not-running-after-restart" \
        900 \
        "tailscaled did not reach Running after its single restart; restart budget remains consumed"
    fi
  elif [ -f "$funnel_restart" ]; then
    restarted_at="$(cat "$funnel_restart")"
    if [ "$(($(date '+%s') - restarted_at))" -ge 120 ]; then
      funnel_error="$(tr '\n' ' ' < "$funnel_health_error" | cut -c 1-300)"
      record_throttled \
        "funnel-still-unavailable" \
        900 \
        "Funnel remains unavailable after its restart; leaving tailscaled running for inspection; error=$funnel_error"
    fi
  fi
fi

available_kib="$(df -k /System/Volumes/Data | awk 'NR == 2 { print $4 }')"
critical_marker="$state_dir/disk-critical"
if [ "$available_kib" -lt $((15 * 1024 * 1024)) ]; then
  if [ ! -f "$critical_marker" ]; then
    touch "$critical_marker"
    record "disk below 15 GiB; stopping write-heavy Qiu Market roles"
    for role in crawler worker dex; do
      launchctl bootout "$launch_domain/com.qiumarket.$role" >/dev/null 2>&1 || true
    done
  fi
elif [ "$available_kib" -ge $((25 * 1024 * 1024)) ] && [ -f "$critical_marker" ]; then
  find "$critical_marker" -maxdepth 0 -type f -delete
  record "disk recovered above 25 GiB; bootstrapping paused roles"
  for role in crawler worker dex; do
    launchctl bootstrap "$launch_domain" "$launch_plist_dir/com.qiumarket.$role.plist" >/dev/null 2>&1 || true
  done
elif [ "$available_kib" -lt $((25 * 1024 * 1024)) ]; then
  record_throttled "disk-warning" 3600 "disk warning: free space below 25 GiB"
fi

for log_file in "$log_dir"/*.log; do
  [ -f "$log_file" ] || continue
  rotate_log "$log_file" $((50 * 1024 * 1024)) $((10 * 1024 * 1024))
done

if [ -f "$production_env" ]; then
  chmod 600 "$production_env"
fi
