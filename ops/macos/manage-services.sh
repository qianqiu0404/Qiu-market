#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="$HOME/Library/Application Support/Qiu Market"
binary_dir="$support_dir/bin"
log_dir="$support_dir/logs"
production_env="$support_dir/production.env"
launch_dir="$HOME/Library/LaunchAgents"
template="$repo_root/ops/macos/com.qiumarket.role.plist.template"
roles=(trading api crawler worker dex)

prepare() {
  mkdir -p "$binary_dir" "$log_dir"
  if [ ! -f "$production_env" ]; then
    cp "$repo_root/ops/macos/production.env.example" "$production_env"
    chmod 600 "$production_env"
    echo "Created private template: $production_env"
    echo "Replace every placeholder before installation." >&2
  fi
  if grep -q 'replace-with-' "$production_env"; then
    echo "Production environment still contains placeholders: $production_env" >&2
    return 1
  fi
  (
    cd "$repo_root"
    go build -trimpath -o "$binary_dir/market-services" ./cmd/market-services
  )
  chmod 700 "$binary_dir/market-services"
  echo "Prepared managed binary: $binary_dir/market-services"
}

render_plist() {
  local role="$1"
  local target="$launch_dir/com.qiumarket.$role.plist"
  sed \
    -e "s|__ROLE__|$role|g" \
    -e "s|__REPO_ROOT__|$repo_root|g" \
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
    echo "Usage: $0 prepare|install|restart|status|uninstall" >&2
    exit 2
    ;;
esac
