#!/usr/bin/env bash
set -euo pipefail

support_dir="/Users/xiuqiu/Library/Application Support/Qiu Market"
state_dir="$support_dir/guardian"
log_dir="$support_dir/logs"
incident_log="$state_dir/incidents.log"
production_env="$support_dir/production.env"
install -d -m 700 "$state_dir" "$log_dir"
touch "$incident_log"
chmod 600 "$incident_log"

if launchctl print system/com.qiumarket.api >/dev/null 2>&1; then
  launch_domain="system"
  launch_plist_dir="/Library/LaunchDaemons"
else
  launch_domain="gui/$(id -u xiuqiu)"
  launch_plist_dir="/Users/xiuqiu/Library/LaunchAgents"
fi

record() {
  printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >> "$incident_log"
}

counter_file="$state_dir/api-failures"
api_failures="$(cat "$counter_file" 2>/dev/null || echo 0)"
if curl --fail --silent --max-time 3 http://127.0.0.1:9092/healthz >/dev/null; then
  echo 0 > "$counter_file"
else
  api_failures=$((api_failures + 1))
  echo "$api_failures" > "$counter_file"
  if [ "$api_failures" -eq 3 ]; then
    record "api failed three consecutive health checks; kickstarting only API"
    launchctl kickstart -k "$launch_domain/com.qiumarket.api" || true
  fi
fi

trading_restart="$state_dir/trading-restart-at"
if nc -z 127.0.0.1 9094 >/dev/null 2>&1; then
  find "$trading_restart" -maxdepth 0 -type f -delete 2>/dev/null || true
elif [ ! -f "$trading_restart" ]; then
  date '+%s' > "$trading_restart"
  record "trading gRPC unavailable; performing the single automatic restart"
  launchctl kickstart -k "$launch_domain/com.qiumarket.trading" || true
else
  restarted_at="$(cat "$trading_restart")"
  if [ "$(($(date '+%s') - restarted_at))" -ge 120 ]; then
    record "trading remains unavailable after its one restart; leaving it offline for inspection"
  fi
fi

if ! pg_isready -q -d s78_market; then
  record "PostgreSQL unavailable; guardian will not blindly restart the shared database"
fi

funnel_origin="${MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"
funnel_cooldown="$state_dir/funnel-restart-at"
if curl --fail --silent --max-time 5 "$funnel_origin/healthz" >/dev/null; then
  find "$funnel_cooldown" -maxdepth 0 -type f -delete 2>/dev/null || true
elif curl --fail --silent --max-time 3 http://127.0.0.1:9092/healthz >/dev/null; then
  last_restart="$(cat "$funnel_cooldown" 2>/dev/null || echo 0)"
  if [ "$(($(date '+%s') - last_restart))" -ge 900 ]; then
    date '+%s' > "$funnel_cooldown"
    record "Funnel unavailable while local API is healthy; restarting only Qiu Market tailscaled"
    launchctl kickstart -k "$launch_domain/com.qiumarket.tailscaled" || true
    sleep 3
    /opt/homebrew/bin/tailscale \
      --socket="/Users/xiuqiu/Library/Application Support/Qiu Market/tailscale/tailscaled.sock" \
      funnel --bg --yes --https=443 http://127.0.0.1:9092 || true
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
  record "disk warning: free space below 25 GiB"
fi

for log_file in "$log_dir"/*.log; do
  [ -f "$log_file" ] || continue
  size="$(stat -f '%z' "$log_file")"
  if [ "$size" -gt $((50 * 1024 * 1024)) ]; then
    cp "$log_file" "$log_file.1"
    tail -c $((10 * 1024 * 1024)) "$log_file.1" > "$log_file"
    chmod 600 "$log_file" "$log_file.1"
    record "rotated $(basename "$log_file") at $size bytes"
  fi
done

if [ -f "$production_env" ]; then
  chmod 600 "$production_env"
fi
