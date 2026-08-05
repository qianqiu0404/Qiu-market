#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_dir="/tmp/s78-market-services-${UID}"
action="${1:-up}"
requested_role="${2:-}"
mkdir -p "$runtime_dir"

all_roles=(api trading rpc crawler worker dex dw frontend)
roles=("${all_roles[@]}")
if [ "${S78_SKIP_DORIS:-0}" = "1" ]; then
  roles=(api trading rpc crawler worker dex frontend)
fi
restart_only=0

requested_terminal_mode="${S78_DEV_TERMINAL_MODE:-auto}"
case "$requested_terminal_mode" in
  auto|iterm|tabs|windows) ;;
  *)
    echo "S78_DEV_TERMINAL_MODE must be one of: auto, iterm, tabs, windows" >&2
    exit 2
    ;;
esac

iterm_usable() {
  osascript -e 'id of application "iTerm2"' >/dev/null 2>&1 &&
    osascript -e 'tell application "iTerm2" to count of windows' >/dev/null 2>&1
}

pid_for() {
  local role="$1"
  local pid_file="$runtime_dir/$role.pid"
  [ -f "$pid_file" ] || return 1
  tr -dc '0-9' < "$pid_file"
}

is_running() {
  local pid
  pid="$(pid_for "$1" 2>/dev/null || true)"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

status() {
  local role pid state
  for role in "${roles[@]}"; do
    pid="$(pid_for "$role" 2>/dev/null || true)"
    state="stopped"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      state="running"
    elif [ -n "$pid" ]; then
      state="stale-pid"
    fi
    printf '%-10s %-10s %s\n' "$role" "$state" "${pid:--}"
  done
}

stop() {
  local role pid
  for role in "${roles[@]}"; do
    pid="$(pid_for "$role" 2>/dev/null || true)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "Stopping $role (PID $pid)"
      kill -TERM "$pid"
    fi
  done
  for _ in {1..20}; do
    local alive=0
    for role in "${roles[@]}"; do
      if is_running "$role"; then alive=1; fi
    done
    [ "$alive" -eq 0 ] && break
    sleep 0.25
  done
  for role in "${roles[@]}"; do
    pid="$(pid_for "$role" 2>/dev/null || true)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "Force stopping $role (PID $pid)"
      kill -KILL "$pid"
    fi
    rm -f "$runtime_dir/$role.pid"
  done
}

case "$action" in
  status)
    status
    exit 0
    ;;
  stop)
    stop
    exit 0
    ;;
  logs)
    log_files=()
    for role in "${roles[@]}"; do
      [ -f "$runtime_dir/$role.log" ] && log_files+=("$runtime_dir/$role.log")
    done
    if [ "${#log_files[@]}" -eq 0 ]; then
      echo "No managed logs exist in $runtime_dir"
      exit 0
    fi
    exec tail -n 100 -f "${log_files[@]}"
    ;;
  restart)
    valid_role=0
    for role in "${all_roles[@]}"; do
      [ "$role" = "$requested_role" ] && valid_role=1
    done
    if [ "$valid_role" -ne 1 ]; then
      echo "Usage: $0 restart {api|trading|rpc|crawler|worker|dex|dw|frontend}" >&2
      exit 2
    fi
    roles=("$requested_role")
    restart_only=1
    if [ "${S78_DEV_DRY_RUN:-0}" != "1" ]; then
      stop
    fi
    ;;
  up) ;;
  *)
    echo "Usage: $0 {up|status|stop|logs|restart ROLE}" >&2
    exit 2
    ;;
esac

if [ "${S78_DEV_DRY_RUN:-0}" = "1" ]; then
  if [ "$restart_only" -eq 1 ]; then
    echo "Dry run: S78 would stop and restart this managed Terminal role:"
  else
    echo "Dry run: S78 would start these Terminal roles:"
  fi
  printf '  - %s\n' "${roles[@]}"
  case "$requested_terminal_mode" in
    auto)
      echo "Terminal layout: auto (prefer iTerm2 one-window tabs; then Terminal tabs/windows fallback)"
      ;;
    iterm)
      echo "Terminal layout: iTerm2 one window with tabs"
      ;;
    tabs)
      echo "Terminal layout: Terminal.app one window with tabs (requires Accessibility)"
      ;;
    windows)
      echo "Terminal layout: Terminal.app separate windows (does not require Accessibility)"
      ;;
  esac
  echo "Runtime state: $runtime_dir"
  echo "Frontend: http://127.0.0.1:5174 (5173 remains untouched)"
  if [ "${S78_CEX_PREVIEW:-1}" = "0" ]; then
    echo "CEX local preview: disabled (formal rollout boundaries apply)"
  else
    echo "CEX local preview: enabled for Binance, Coinbase, Bybit, and OKX"
  fi
  if [ "${S78_DEX_PREVIEW:-1}" = "0" ]; then
    echo "DEX local preview: disabled (formal rollout boundaries apply)"
  else
    echo "DEX local preview: enabled for Hyperliquid, Uniswap, and PancakeSwap"
  fi
  exit 0
fi

cd "$root_dir"
[ -f .env ] || { echo "Missing $root_dir/.env" >&2; exit 1; }

for command in go node npm pg_isready psql pg_dump redis-cli lsof osascript; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "Required command is missing: $command" >&2
    exit 1
  }
done

terminal_mode="$requested_terminal_mode"
if [ "$terminal_mode" = "auto" ]; then
  if iterm_usable; then
    terminal_mode="iterm"
  else
    ui_enabled="$(osascript -e 'tell application "System Events" to UI elements enabled' 2>/dev/null || true)"
    if [ "$ui_enabled" = "true" ]; then
      terminal_mode="tabs"
    else
      terminal_mode="windows"
    fi
  fi
fi
if [ "$terminal_mode" = "iterm" ] && ! iterm_usable; then
  echo "iTerm2 is unavailable or macOS Automation access was denied." >&2
  echo "Install/allow iTerm2, or use S78_DEV_TERMINAL_MODE=windows." >&2
  exit 1
fi
if [ "$terminal_mode" = "tabs" ]; then
  ui_enabled="$(osascript -e 'tell application "System Events" to UI elements enabled' 2>/dev/null || true)"
  if [ "$ui_enabled" != "true" ]; then
    echo "Terminal.app tabs require macOS Accessibility permission." >&2
    echo "Use the default 'make dev' for iTerm2/permission-free fallback," >&2
    echo "or enable Codex/Terminal in System Settings → Privacy & Security → Accessibility." >&2
    exit 1
  fi
fi
if [ "$terminal_mode" = "auto" ]; then
  # Defensive fallback; the auto branch above always resolves the mode.
  if [ "${ui_enabled:-false}" = "true" ]; then
    terminal_mode="tabs"
  else
    terminal_mode="windows"
  fi
fi

for role in "${roles[@]}"; do
  if is_running "$role"; then
    echo "Managed role $role is already running. Use 'make dev-status' or 'make dev-stop'." >&2
    exit 1
  fi
done

# shellcheck disable=SC1091
source .env
: "${MARKET_MASTER_DB_HOST:=127.0.0.1}"
: "${MARKET_MASTER_DB_PORT:=5432}"
: "${MARKET_MASTER_DB_USER:=$(id -un)}"
: "${MARKET_MASTER_DB_NAME:=s78_market}"
: "${MARKET_HTTP_PORT:=9092}"
: "${MARKET_RPC_PORT:=9091}"
: "${MARKET_TRADING_GRPC_ADDR:=127.0.0.1:9094}"
: "${MARKET_REDIS_ADDRESS:=127.0.0.1:6379}"

ports=()
for role in "${roles[@]}"; do
  case "$role" in
    api) ports+=("$MARKET_HTTP_PORT") ;;
    trading) ports+=("${MARKET_TRADING_GRPC_ADDR##*:}") ;;
    rpc) ports+=("$MARKET_RPC_PORT") ;;
    frontend) ports+=(5174) ;;
  esac
done
if [ "${#ports[@]}" -gt 0 ]; then
  for port in "${ports[@]}"; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "Port $port is already owned by an unmanaged process:" >&2
      lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2
      echo "The launcher will not kill it automatically." >&2
      exit 1
    fi
  done
fi

backend_selected=0
frontend_selected=0
for role in "${roles[@]}"; do
  [ "$role" = "frontend" ] && frontend_selected=1
  [ "$role" != "frontend" ] && backend_selected=1
done

if [ "$backend_selected" -eq 1 ]; then
  pg_isready \
    -h "$MARKET_MASTER_DB_HOST" \
    -p "$MARKET_MASTER_DB_PORT" \
    -U "$MARKET_MASTER_DB_USER" \
    -d "$MARKET_MASTER_DB_NAME" >/dev/null

  redis_host="${MARKET_REDIS_ADDRESS%:*}"
  redis_port="${MARKET_REDIS_ADDRESS##*:}"
  if [ -n "${MARKET_REDIS_PASSWORD:-}" ]; then
    REDISCLI_AUTH="$MARKET_REDIS_PASSWORD" \
      redis-cli -h "$redis_host" -p "$redis_port" -n "${MARKET_REDIS_DB_INDEX:-0}" ping >/dev/null
  else
    redis-cli -h "$redis_host" -p "$redis_port" -n "${MARKET_REDIS_DB_INDEX:-0}" ping >/dev/null
  fi

  echo "Building S78 backend once..."
  go build -o market-services ./cmd/market-services
fi

if [ "$frontend_selected" -eq 1 ] && [ ! -d frontend/node_modules ]; then
  echo "Installing frontend dependencies..."
  (cd frontend && npm install)
fi

if [ "$restart_only" -eq 0 ]; then
export PGPASSWORD="${MARKET_MASTER_DB_PASSWORD:-}"
migrations_ready="$(
  psql \
    -h "$MARKET_MASTER_DB_HOST" \
    -p "$MARKET_MASTER_DB_PORT" \
    -U "$MARKET_MASTER_DB_USER" \
    -d "$MARKET_MASTER_DB_NAME" \
    -Atqc "SELECT
      EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='symbol_kline' AND column_name='open_time'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='symbol_market' AND column_name='change_24h_pct'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='market_provider_status'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='kline_repair_task'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='provider_market_candidate'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='asset_price_index'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='provider_rollout_state'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='asset_venue_snapshot'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='dex_route_current'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='asset_identity_merge_audit'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='exchange_symbol' AND column_name='kline_enabled'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='kline_scope_cleanup_audit'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='symbol_market' AND column_name='open_24h'
      )
      AND EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname='provider_rollout_canary_assets_check'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='provider_rollout_state'
          AND column_name='local_preview_enabled'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='provider_asset_selection_state'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='provider_asset_selection'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='provider_kline_selection'
      )
      AND EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname='provider_asset_selection_state_provider_check'
          AND pg_get_constraintdef(oid) ILIKE '%pancakeswap%'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='asset_venue_snapshot'
          AND column_name='last_success_at'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='dex_route_current'
          AND column_name='quote_reference_kind'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='dex_route_current'
          AND column_name='protocol_versions'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='dex_quote_observation'
          AND column_name='quote_notional_usd'
      )
      AND EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_name='trading_market'
      )"
)"
if [ "$migrations_ready" != "t" ]; then
  backup_dir="${HOME}/Library/Application Support/S78 Market Services/backups"
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  backup_file="$backup_dir/s78_market-before-pending-migrations-$(date '+%Y%m%d-%H%M%S').dump"
  echo "Creating a private pre-migration backup..."
  pg_dump \
    -h "$MARKET_MASTER_DB_HOST" \
    -p "$MARKET_MASTER_DB_PORT" \
    -U "$MARKET_MASTER_DB_USER" \
    -d "$MARKET_MASTER_DB_NAME" \
    -Fc \
    -f "$backup_file"
  chmod 600 "$backup_file"
  echo "Backup created: $backup_file"
fi

echo "Running idempotent PostgreSQL migrations..."
./market-services migrate

if [ "${S78_CEX_PREVIEW:-1}" = "0" ]; then
  echo "Disabling CEX local preview; formal rollout boundaries will be restored..."
  S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
    --provider binance,coinbase,bybit,okx \
    --disable
else
  echo "Enabling CEX local preview for the four spot providers..."
  S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
    --provider binance,coinbase,bybit,okx \
    --enable
fi

if [ "${S78_DEX_PREVIEW:-1}" = "0" ]; then
  echo "Disabling DEX local preview; formal rollout boundaries will be restored..."
  S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
    --provider hyperliquid,uniswap,pancakeswap \
    --disable
else
  echo "Enabling DEX local preview for Hyperliquid, Uniswap, and PancakeSwap..."
  S78_LOCAL_PREVIEW=1 ./market-services catalog preview \
    --provider hyperliquid,uniswap,pancakeswap \
    --enable
fi
fi

if [ "$restart_only" -eq 0 ] && [ "${S78_SKIP_DORIS:-0}" != "1" ]; then
  command -v docker >/dev/null 2>&1 || {
    echo "Docker is required for Doris. Use S78_SKIP_DORIS=1 make dev for the core stack." >&2
    exit 1
  }
  echo "Starting Doris..."
  docker compose up -d doris
  for _ in {1..60}; do
    health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' s78-market-doris 2>/dev/null || true)"
    [ "$health" = "healthy" ] && break
    sleep 2
  done
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' s78-market-doris 2>/dev/null || true)"
  [ "$health" = "healthy" ] || {
    echo "Doris did not become healthy. Use 'docker compose logs doris' for details." >&2
    exit 1
  }
  docker exec -i s78-market-doris mysql -h127.0.0.1 -P9030 -uroot < script/doris-init.sql
fi

shell_quote() {
  printf '%q' "$1"
}

role_command() {
  local role="$1"
  printf 'cd %s && exec bash script/dev-role.sh %s' "$(shell_quote "$root_dir")" "$(shell_quote "$role")"
}

if [ "$terminal_mode" = "iterm" ]; then
  if osascript - "${roles[@]}" "$root_dir" <<'APPLESCRIPT'
on run argv
  set projectRoot to item (count argv) of argv
  tell application "iTerm2"
    activate
    set serviceWindow to (create window with default profile)
    repeat with i from 1 to (count argv) - 1
      set roleName to item i of argv
      set roleCommand to "cd " & quoted form of projectRoot & " && exec bash script/dev-role.sh " & quoted form of roleName
      if i is 1 then
        set serviceSession to current session of serviceWindow
      else
        tell serviceWindow
          set serviceTab to (create tab with default profile)
        end tell
        set serviceSession to current session of serviceTab
      end if
      tell serviceSession
        write text roleCommand
      end tell
      delay 0.2
    end repeat
    tell first tab of serviceWindow to select
  end tell
end run
APPLESCRIPT
  then
    echo "S78 development terminals opened in one iTerm2 window with tabs."
  else
    echo "iTerm2 automation failed; one or more roles may have started." >&2
    echo "Run 'make dev-status' and use 'make dev-stop' before retrying." >&2
    exit 1
  fi
fi

if [ "$terminal_mode" = "tabs" ]; then
  first_command="$(role_command "${roles[0]}")"
  osascript - "$first_command" "${roles[@]:1}" "$root_dir" <<'APPLESCRIPT'
on run argv
  set firstCommand to item 1 of argv
  tell application "Terminal"
    activate
    do script firstCommand
  end tell
  delay 0.4
  repeat with i from 2 to (count argv) - 1
    set roleName to item i of argv
    set projectRoot to item (count argv) of argv
    tell application "System Events"
      tell process "Terminal"
        keystroke "t" using command down
      end tell
    end tell
    delay 0.2
    set roleCommand to "cd " & quoted form of projectRoot & " && exec bash script/dev-role.sh " & quoted form of roleName
    tell application "Terminal"
      do script roleCommand in selected tab of front window
    end tell
    delay 0.2
  end repeat
end run
APPLESCRIPT
  echo "S78 development terminals opened in one Terminal.app window with tabs."
elif [ "$terminal_mode" = "windows" ]; then
  osascript - "${roles[@]}" "$root_dir" <<'APPLESCRIPT'
on run argv
  set projectRoot to item (count argv) of argv
  tell application "Terminal"
    activate
    repeat with i from 1 to (count argv) - 1
      set roleName to item i of argv
      set roleCommand to "cd " & quoted form of projectRoot & " && exec bash script/dev-role.sh " & quoted form of roleName
      do script roleCommand
      delay 0.2
    end repeat
  end tell
end run
APPLESCRIPT
  echo "S78 development terminals opened as separate Terminal.app windows."
fi

# AppleScript returns when it has submitted commands to the terminal, not when
# dev-role.sh has written each PID file. Wait for the managed roles so an
# immediate `make dev-status` cannot report a false stopped state.
startup_failed=()
for role in "${roles[@]}"; do
  for _ in {1..40}; do
    is_running "$role" && break
    sleep 0.25
  done
  if ! is_running "$role"; then
    startup_failed+=("$role")
  fi
done
if [ "${#startup_failed[@]}" -gt 0 ]; then
  echo "These managed roles did not reach running state: ${startup_failed[*]}" >&2
  echo "Inspect logs with 'make dev-logs' before retrying." >&2
  exit 1
fi

echo "Frontend: http://127.0.0.1:5174"
if [ "$restart_only" -eq 1 ]; then
  echo "Restarted managed role: $requested_role"
fi
echo "Use 'make dev-status', 'make dev-logs', 'make dev-restart ROLE=crawler', or 'make dev-stop'."
