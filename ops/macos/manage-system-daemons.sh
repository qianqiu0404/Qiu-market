#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
log_dir="/Users/xiuqiu/Library/Application Support/Qiu Market/logs"
daemon_dir="/Library/LaunchDaemons"
roles=(trading api crawler worker dex)

render_role() {
  local role="$1"
  sed \
    -e "s|__ROLE__|$role|g" \
    -e "s|__REPO_ROOT__|$repo_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    "$repo_root/ops/macos/com.qiumarket.role.daemon.plist.template" |
    sudo tee "$daemon_dir/com.qiumarket.$role.plist" >/dev/null
}

render_backup() {
  local mode="$1"
  local schedule
  if [ "$mode" = "full" ]; then
    schedule='<key>StartCalendarInterval</key><dict><key>Hour</key><integer>2</integer><key>Minute</key><integer>15</integer></dict>'
  else
    schedule='<key>StartInterval</key><integer>3600</integer><key>RunAtLoad</key><true/>'
  fi
  sed \
    -e "s|__MODE__|$mode|g" \
    -e "s|__REPO_ROOT__|$repo_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    -e "s|<!-- __SCHEDULE__ -->|$schedule|g" \
    "$repo_root/ops/macos/com.qiumarket.backup.plist.template" |
    sudo tee "$daemon_dir/com.qiumarket.backup.$mode.plist" >/dev/null
}

case "$action" in
  install)
    "$repo_root/ops/macos/manage-services.sh" prepare
    install -d -m 700 "$log_dir"
    for role in "${roles[@]}"; do
      render_role "$role"
    done
    sed \
      -e "s|__REPO_ROOT__|$repo_root|g" \
      -e "s|__LOG_DIR__|$log_dir|g" \
      "$repo_root/ops/macos/com.qiumarket.guardian.plist.template" |
      sudo tee "$daemon_dir/com.qiumarket.guardian.plist" >/dev/null
    sed \
      -e "s|__REPO_ROOT__|$repo_root|g" \
      -e "s|__LOG_DIR__|$log_dir|g" \
      "$repo_root/ops/macos/com.qiumarket.tailscaled.daemon.plist.template" |
      sudo tee "$daemon_dir/com.qiumarket.tailscaled.plist" >/dev/null
    sed \
      -e "s|__REPO_ROOT__|$repo_root|g" \
      -e "s|__LOG_DIR__|$log_dir|g" \
      "$repo_root/ops/macos/com.qiumarket.restore-drill.plist.template" |
      sudo tee "$daemon_dir/com.qiumarket.restore-drill.plist" >/dev/null
    render_backup full
    render_backup trading
    sudo chown root:wheel "$daemon_dir"/com.qiumarket.*.plist
    sudo chmod 644 "$daemon_dir"/com.qiumarket.*.plist
    for label in "${roles[@]}"; do
      launchctl bootout "gui/$UID/com.qiumarket.$label" >/dev/null 2>&1 || true
      sudo launchctl bootout "system/com.qiumarket.$label" >/dev/null 2>&1 || true
      sudo launchctl bootstrap system "$daemon_dir/com.qiumarket.$label.plist"
    done
    launchctl bootout "gui/$UID/com.qiumarket.tailscaled" >/dev/null 2>&1 || true
    sudo launchctl bootout "system/com.qiumarket.tailscaled" >/dev/null 2>&1 || true
    sudo launchctl bootstrap system "$daemon_dir/com.qiumarket.tailscaled.plist"
    for label in guardian backup.full backup.trading restore-drill; do
      sudo launchctl bootout "system/com.qiumarket.$label" >/dev/null 2>&1 || true
      sudo launchctl bootstrap system "$daemon_dir/com.qiumarket.$label.plist"
    done
    sleep 3
    /opt/homebrew/bin/tailscale \
      --socket="/Users/xiuqiu/Library/Application Support/Qiu Market/tailscale/tailscaled.sock" \
      funnel --bg --yes --https=443 http://127.0.0.1:9092
    sudo pmset -a autorestart 1
    echo "Installed Qiu Market system LaunchDaemons and enabled restart after power return."
    ;;
  status)
    for label in "${roles[@]}" tailscaled guardian backup.full backup.trading restore-drill; do
      if sudo -n launchctl print "system/com.qiumarket.$label" >/dev/null 2>&1; then
        printf '%-20s managed\n' "$label"
      else
        printf '%-20s not-installed-or-auth-required\n' "$label"
      fi
    done
    pmset -g custom | grep autorestart || true
    ;;
  *)
    echo "Usage: $0 install|status" >&2
    exit 2
    ;;
esac
