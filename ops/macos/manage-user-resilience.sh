#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="/Users/xiuqiu/Library/Application Support/Qiu Market"
log_dir="$support_dir/logs"
launch_dir="/Users/xiuqiu/Library/LaunchAgents"
domain="gui/$UID"

render_backup() {
  local mode="$1"
  local schedule
  if [ "$mode" = "full" ]; then
    schedule='<key>StartCalendarInterval</key><dict><key>Hour</key><integer>2</integer><key>Minute</key><integer>15</integer></dict>'
  else
    schedule='<key>StartInterval</key><integer>3600</integer><key>RunAtLoad</key><true/>'
  fi
  sed \
    -e '/<key>UserName<\/key>/{N;d;}' \
    -e "s|__MODE__|$mode|g" \
    -e "s|__REPO_ROOT__|$repo_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    -e "s|<!-- __SCHEDULE__ -->|$schedule|g" \
    "$repo_root/ops/macos/com.qiumarket.backup.plist.template" \
    > "$launch_dir/com.qiumarket.backup.$mode.plist"
}

case "$action" in
  install)
    install -d -m 700 "$support_dir" "$log_dir" "$launch_dir"
    sed \
      -e "s|__REPO_ROOT__|$repo_root|g" \
      -e "s|__LOG_DIR__|$log_dir|g" \
      "$repo_root/ops/macos/com.qiumarket.guardian.plist.template" \
      > "$launch_dir/com.qiumarket.guardian.plist"
    render_backup full
    render_backup trading
    sed \
      -e '/<key>UserName<\/key>/{N;d;}' \
      -e "s|__REPO_ROOT__|$repo_root|g" \
      -e "s|__LOG_DIR__|$log_dir|g" \
      "$repo_root/ops/macos/com.qiumarket.restore-drill.plist.template" \
      > "$launch_dir/com.qiumarket.restore-drill.plist"
    for label in guardian backup.full backup.trading restore-drill; do
      plutil -lint "$launch_dir/com.qiumarket.$label.plist" >/dev/null
      launchctl bootout "$domain/com.qiumarket.$label" >/dev/null 2>&1 || true
      launchctl bootstrap "$domain" "$launch_dir/com.qiumarket.$label.plist"
    done
    echo "Installed password-free user resilience jobs. System-daemon promotion still requires administrator authentication."
    ;;
  status)
    for label in guardian backup.full backup.trading restore-drill; do
      if launchctl print "$domain/com.qiumarket.$label" >/dev/null 2>&1; then
        printf '%-20s managed\n' "$label"
      else
        printf '%-20s not-installed\n' "$label"
      fi
    done
    ;;
  *)
    echo "Usage: $0 install|status" >&2
    exit 2
    ;;
esac
