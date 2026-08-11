#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_root="${QIU_MARKET_LIVE_RUNTIME_ROOT:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
log_dir="$runtime_root/logs"
launch_dir="${QIU_MARKET_LIVE_LAUNCH_DIR:-$runtime_root/launchd-live}"
launch_domain="${QIU_MARKET_LIVE_LAUNCH_DOMAIN:-gui/$UID}"
label="com.qiu-market.live.log-rotation"
target="$launch_dir/$label.plist"
installed_script="$runtime_root/ops/live-log-rotation"
template="$repo_root/ops/macos/com.qiu-market.live.log-rotation.plist.template"

render() {
  local temporary
  [ -d "$runtime_root" ] || {
    echo "Qiu Market d1-candidate runtime is unavailable: $runtime_root" >&2
    return 1
  }
  install -d -m 700 "$runtime_root/ops" "$log_dir" "$launch_dir"
  install -m 700 "$repo_root/ops/macos/rotate-live-logs.sh" "$installed_script"
  temporary="$(mktemp "$launch_dir/.log-rotation.plist.XXXXXX")"
  sed \
    -e "s|__RUNTIME_ROOT__|$runtime_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    "$template" > "$temporary"
  chmod 600 "$temporary"
  plutil -lint "$temporary" >/dev/null
  mv -f "$temporary" "$target"
}

case "$action" in
  install)
    render
    launchctl bootout "$launch_domain/$label" >/dev/null 2>&1 || true
    launchctl bootstrap "$launch_domain" "$target"
    echo "Installed bounded Qiu Market live-log rotation."
    ;;
  status)
    if launchctl print "$launch_domain/$label" >/dev/null 2>&1; then
      echo "log_rotation_status=managed"
    else
      echo "log_rotation_status=not-installed"
    fi
    ;;
  uninstall)
    launchctl bootout "$launch_domain/$label" >/dev/null 2>&1 || true
    if [ -f "$target" ] && [ ! -L "$target" ]; then
      find "$target" -maxdepth 0 -type f -delete
    fi
    if [ -f "$installed_script" ] && [ ! -L "$installed_script" ]; then
      find "$installed_script" -maxdepth 0 -type f -delete
    fi
    echo "Removed the log-rotation LaunchAgent; existing logs were preserved."
    ;;
  *)
    echo "Usage: $0 install|status|uninstall" >&2
    exit 2
    ;;
esac
