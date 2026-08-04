#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_root="${QIU_MARKET_RUNTIME_ROOT:-$repo_root}"
support_dir="$HOME/Library/Application Support/Qiu Market"
binary_dir="$support_dir/bin"
log_dir="$support_dir/logs"
production_env="$support_dir/production.env"
launch_dir="$HOME/Library/LaunchAgents"
template="$repo_root/ops/macos/com.qiumarket.role.plist.template"
release_tool="$repo_root/ops/macos/release-production.sh"
roles=(trading api crawler worker dex)

prepare() {
  mkdir -p "$log_dir"
  if [ ! -f "$production_env" ]; then
    cp "$repo_root/ops/macos/production.env.example" "$production_env"
    chmod 600 "$production_env"
    echo "Created private template: $production_env"
    echo "Replace every placeholder before installation." >&2
  fi
  "$release_tool" prepare
  echo "Release staged only. Use release-production.sh verify and deploy to activate it."
}

render_plist() {
  local role="$1"
  local target="$launch_dir/com.qiumarket.$role.plist"
  sed \
    -e "s|__ROLE__|$role|g" \
    -e "s|__REPO_ROOT__|$runtime_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    "$template" > "$target"
  plutil -lint "$target" >/dev/null
}

case "$action" in
  prepare)
    prepare
    ;;
  install)
    prepare
    if [ ! -x "$binary_dir/market-services" ]; then
      echo "No active managed binary exists. Run release-production.sh verify and deploy first." >&2
      exit 1
    fi
    if lsof -nP -iTCP:9092 -sTCP:LISTEN >/dev/null 2>&1 ||
      lsof -nP -iTCP:9094 -sTCP:LISTEN >/dev/null 2>&1; then
      echo "Ports 9092/9094 are already in use. Stop the current Qiu Market dev roles first." >&2
      exit 1
    fi
    mkdir -p "$launch_dir"
    for role in "${roles[@]}"; do
      render_plist "$role"
      launchctl bootout "gui/$UID/com.qiumarket.$role" >/dev/null 2>&1 || true
      launchctl bootstrap "gui/$UID" "$launch_dir/com.qiumarket.$role.plist"
    done
    echo "Installed Qiu Market LaunchAgents."
    ;;
  restart)
    for role in "${roles[@]}"; do
      launchctl kickstart -k "gui/$UID/com.qiumarket.$role"
    done
    ;;
  reload)
    if [ ! -x "$binary_dir/market-services" ]; then
      echo "Managed binary is missing. Use release-production.sh verify and deploy first." >&2
      exit 1
    fi
    mkdir -p "$launch_dir"
    for role in "${roles[@]}"; do
      render_plist "$role"
    done
    for role in "${roles[@]}"; do
      launchctl bootout "gui/$UID/com.qiumarket.$role" >/dev/null 2>&1 || true
      bootstrapped=false
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        if launchctl bootstrap "gui/$UID" "$launch_dir/com.qiumarket.$role.plist" >/dev/null 2>&1; then
          bootstrapped=true
          break
        fi
        sleep 1
      done
      if [ "$bootstrapped" != true ]; then
        echo "Failed to bootstrap Qiu Market role after reload: $role" >&2
        exit 1
      fi
    done
    echo "Reloaded Qiu Market LaunchAgents with current definitions and unchanged binary."
    ;;
  status)
    for role in "${roles[@]}"; do
      if launchctl print "gui/$UID/com.qiumarket.$role" >/dev/null 2>&1; then
        printf '%-10s managed\n' "$role"
      else
        printf '%-10s not-installed\n' "$role"
      fi
    done
    "$repo_root/ops/macos/verify-runtime.sh" || true
    ;;
  uninstall)
    for role in "${roles[@]}"; do
      launchctl bootout "gui/$UID/com.qiumarket.$role" >/dev/null 2>&1 || true
    done
    echo "LaunchAgents stopped; private environment, binary, and logs were preserved."
    ;;
  *)
    echo "Usage: $0 prepare|install|restart|reload|status|uninstall" >&2
    exit 2
    ;;
esac
