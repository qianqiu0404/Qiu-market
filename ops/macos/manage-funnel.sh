#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_root="${QIU_MARKET_RUNTIME_ROOT:-$repo_root}"
support_dir="$HOME/Library/Application Support/Qiu Market"
tailscale_dir="$support_dir/tailscale"
log_dir="$support_dir/logs"
socket="$tailscale_dir/tailscaled.sock"
tailscale_cli="/opt/homebrew/bin/tailscale"
tailscaled="/opt/homebrew/bin/tailscaled"
launch_plist="$HOME/Library/LaunchAgents/com.qiumarket.tailscaled.plist"
template="$repo_root/ops/macos/com.qiumarket.tailscaled.plist.template"

if [ ! -x "$tailscale_cli" ] || [ ! -x "$tailscaled" ]; then
  echo "Install the Homebrew tailscale formula before configuring Qiu Market." >&2
  exit 1
fi

install_daemon() {
  mkdir -p "$tailscale_dir" "$log_dir" "$HOME/Library/LaunchAgents"
  sed \
    -e "s|__REPO_ROOT__|$runtime_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    "$template" > "$launch_plist"
  plutil -lint "$launch_plist" >/dev/null
  launchctl bootout "gui/$UID/com.qiumarket.tailscaled" >/dev/null 2>&1 || true
  bootstrapped=false
  for _ in $(seq 1 10); do
    if launchctl bootstrap "gui/$UID" "$launch_plist" >/dev/null 2>&1; then
      bootstrapped=true
      break
    fi
    sleep 1
  done
  if [ "$bootstrapped" != true ]; then
    echo "Qiu Market tailscaled LaunchAgent could not be bootstrapped." >&2
    return 1
  fi
  for _ in $(seq 1 10); do
    [ -S "$socket" ] && return
    sleep 1
  done
  echo "Qiu Market tailscaled did not create its socket" >&2
  exit 1
}

ts() {
  "$tailscale_cli" --socket="$socket" "$@"
}

wait_for_running() {
  local state="NoState"
  for _ in $(seq 1 30); do
    state="$(ts status --json 2>/dev/null | jq -r '.BackendState // "NoState"' 2>/dev/null || echo NoState)"
    if [ "$state" = Running ]; then
      return 0
    fi
    sleep 1
  done
  echo "Qiu Market tailscaled did not reach Running; state=$state" >&2
  return 1
}

case "$action" in
  install-daemon)
    install_daemon
    ts version
    ;;
  login)
    [ -S "$socket" ] || install_daemon
    ts up
    ;;
  start)
    curl --fail --silent --max-time 3 http://127.0.0.1:9092/healthz >/dev/null
    [ -S "$socket" ] || install_daemon
    wait_for_running
    ts funnel --bg --yes --https=443 http://127.0.0.1:9092
    ts funnel status
    ;;
  status)
    [ -S "$socket" ] || {
      echo "Qiu Market tailscaled is not installed." >&2
      exit 1
    }
    ts status
    ts funnel status
    ;;
  stop)
    ts funnel --https=443 off
    ;;
  uninstall-daemon)
    launchctl bootout "gui/$UID/com.qiumarket.tailscaled" >/dev/null 2>&1 || true
    echo "Qiu Market tailscaled stopped; its state was preserved."
    ;;
  *)
    echo "Usage: $0 install-daemon|login|start|status|stop|uninstall-daemon" >&2
    exit 2
    ;;
esac
