#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_root="${QIU_MARKET_RUNTIME_ROOT:-$repo_root}"
support_dir="$HOME/Library/Application Support/Qiu Market"
log_dir="$support_dir/logs"
observation_dir="$support_dir/observations"
epoch_file="$observation_dir/acceptance-epoch.json"
launch_dir="$HOME/Library/LaunchAgents"
label="com.qiumarket.observer"
target="$launch_dir/$label.plist"
template="$repo_root/ops/macos/com.qiumarket.observer.plist.template"

render() {
  mkdir -p "$launch_dir" "$log_dir" "$observation_dir"
  sed \
    -e "s|__REPO_ROOT__|$runtime_root|g" \
    -e "s|__LOG_DIR__|$log_dir|g" \
    "$template" > "$target"
  plutil -lint "$target" >/dev/null
}

install_observer() {
  render
  launchctl bootout "gui/$UID/$label" >/dev/null 2>&1 || true
  local installed=false
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if launchctl bootstrap "gui/$UID" "$target" >/dev/null 2>&1; then
      installed=true
      break
    fi
    sleep 1
  done
  if [ "$installed" != true ]; then
    echo "Failed to install Qiu Market production observer." >&2
    return 1
  fi
  echo "Installed Qiu Market production observer (on UTC wall-clock minute boundaries)."
}

case "$action" in
  install)
    install_observer
    ;;
  run)
    "$repo_root/ops/macos/observe-production.sh"
    ;;
  status)
    if launchctl print "gui/$UID/$label" >/dev/null 2>&1; then
      echo "observer: managed"
    else
      echo "observer: not-installed"
    fi
    if [ -f "$observation_dir/latest.json" ]; then
      jq '{
        schema_version,
        acceptance_epoch_id,
        acceptance_eligible,
        deployment_id,
        deployment_url,
        deployment_commit,
        scheduled_at,
        started_at,
        finished_at,
        duration_ms,
        schedule_lag_ms,
        observed_at,
        status,
        current_checks_status,
        historical_acceptance_status,
        checks,
        dex,
        historical_windows
      }' "$observation_dir/latest.json"
    else
      echo "No production observation has been recorded yet."
    fi
    if [ -f "$epoch_file" ]; then
      echo "acceptance epoch:"
      jq '{
        epoch_id,
        status,
        deployment_id,
        deployment_url,
        deployment_commit,
        started_at,
        stopped_at
      }' "$epoch_file"
    else
      echo "acceptance epoch: not-started"
    fi
    ;;
  uninstall)
    launchctl bootout "gui/$UID/$label" >/dev/null 2>&1 || true
    echo "Observer stopped; observation history was preserved."
    ;;
  *)
    echo "Usage: $0 install|run|status|uninstall" >&2
    exit 2
    ;;
esac
