#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime_root="${QIU_MARKET_LIVE_RUNTIME_ROOT:-$HOME/Library/Application Support/Qiu Market/d1-candidate}"
launch_domain="${QIU_MARKET_LIVE_LAUNCH_DOMAIN:-gui/$UID}"
user_launch_dir="${QIU_MARKET_LIVE_USER_LAUNCH_DIR:-$HOME/Library/LaunchAgents}"
test_mode="${QIU_MARKET_LIVE_USER_RUNTIME_TEST_MODE:-false}"
launchctl_bin="${QIU_MARKET_LIVE_LAUNCHCTL_BIN:-/bin/launchctl}"
keepawake_label='com.qiu-market.live.keepawake'
keepawake_source="$runtime_root/launchd-live/$keepawake_label.plist"
keepawake_template="$repo_root/ops/macos/com.qiu-market.live.keepawake.plist.template"

labels=(
  com.qiu-market.d1r1.frontdoor
  com.qiu-market.d1r1.stack
  com.qiu-market.live.crawler
  com.qiu-market.live.dex
  com.qiu-market.live.worker
  com.qiu-market.live.api-tunnel
  com.qiu-market.live.log-rotation
  "$keepawake_label"
)

die() { echo "$*" >&2; exit 1; }

require_test_override_safety() {
  if [ "$test_mode" = true ]; then
    case "$runtime_root:$user_launch_dir" in
      /tmp/qiu-market-live-user-runtime.*:/tmp/qiu-market-live-user-runtime.*) ;;
      *) die 'test mode paths must stay inside an isolated /tmp/qiu-market-live-user-runtime.* fixture' ;;
    esac
  elif [ -n "${QIU_MARKET_LIVE_USER_LAUNCH_DIR:-}" ] || [ -n "${QIU_MARKET_LIVE_LAUNCHCTL_BIN:-}" ]; then
    die 'launch directory and launchctl overrides are fixture-only'
  fi
}

source_plist() {
  local label="$1"
  case "$label" in
    com.qiu-market.d1r1.frontdoor|com.qiu-market.d1r1.stack)
      printf '%s/launchd-r1/%s.plist\n' "$runtime_root" "$label" ;;
    *) printf '%s/launchd-live/%s.plist\n' "$runtime_root" "$label" ;;
  esac
}

private_regular_file() {
  local file="$1" mode owner
  [ -f "$file" ] && [ ! -L "$file" ] || return 1
  mode="$(stat -f '%Lp' "$file")"
  owner="$(stat -f '%u' "$file")"
  [ "$owner" = "$UID" ] && [ "$mode" = 600 ]
}

render_keepawake() {
  local temporary
  [ -d "$runtime_root" ] || die "Qiu Market live runtime is unavailable: $runtime_root"
  install -d -m 700 "$runtime_root/launchd-live" "$runtime_root/logs"
  temporary="$(mktemp "$runtime_root/launchd-live/.keepawake.plist.XXXXXX")"
  sed -e "s|__LOG_DIR__|$runtime_root/logs|g" "$keepawake_template" > "$temporary"
  chmod 600 "$temporary"
  plutil -lint "$temporary" >/dev/null
  mv -f "$temporary" "$keepawake_source"
}

validate_source() {
  local label="$1" source="$2" program
  private_regular_file "$source" || die "unsafe or missing managed LaunchAgent source: $source"
  plutil -lint "$source" >/dev/null
  [ "$(/usr/libexec/PlistBuddy -c 'Print :Label' "$source")" = "$label" ] ||
    die "LaunchAgent label mismatch: $source"
  [ "$(/usr/libexec/PlistBuddy -c 'Print :RunAtLoad' "$source")" = true ] ||
    die "LaunchAgent must use RunAtLoad: $label"
  if [ "$label" = "$keepawake_label" ]; then
    program="$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$source")"
    [ "$program" = /usr/bin/caffeinate ] || die 'keep-awake must use the system caffeinate binary'
    [ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$source")" = -i ] ||
      die 'keep-awake must only prevent idle system sleep'
  fi
}

install_plist() {
  local label="$1" source="$2" target temporary
  target="$user_launch_dir/$label.plist"
  temporary="$(mktemp "$user_launch_dir/.$label.XXXXXX")"
  install -m 600 "$source" "$temporary"
  mv -f "$temporary" "$target"
}

validate_target() {
  local label="$1" target owner
  target="$user_launch_dir/$label.plist"
  if [ -e "$target" ] || [ -L "$target" ]; then
    [ -f "$target" ] && [ ! -L "$target" ] ||
      die "refusing unsafe existing LaunchAgent target: $target"
    owner="$(stat -f '%u' "$target")"
    [ "$owner" = "$UID" ] || die "refusing LaunchAgent target owned by another user: $target"
  fi
}

install_runtime() {
  local label source
  render_keepawake
  install -d -m 700 "$user_launch_dir"
  for label in "${labels[@]}"; do
    source="$(source_plist "$label")"
    validate_source "$label" "$source"
    validate_target "$label"
  done
  for label in "${labels[@]}"; do
    source="$(source_plist "$label")"
    install_plist "$label" "$source"
  done
  if ! "$launchctl_bin" print "$launch_domain/$keepawake_label" >/dev/null 2>&1; then
    "$launchctl_bin" bootstrap "$launch_domain" "$user_launch_dir/$keepawake_label.plist"
  fi
  echo 'Installed Qiu Market login recovery and idle-sleep prevention.'
}

status_runtime() {
  local label source target installed=0 loaded=0 drift=0
  for label in "${labels[@]}"; do
    source="$(source_plist "$label")"
    target="$user_launch_dir/$label.plist"
    if private_regular_file "$source" && private_regular_file "$target" && cmp -s "$source" "$target"; then
      installed=$((installed + 1))
    else
      drift=$((drift + 1))
    fi
    if "$launchctl_bin" print "$launch_domain/$label" >/dev/null 2>&1; then
      loaded=$((loaded + 1))
    fi
  done
  printf 'live_user_runtime_installed=%d/%d loaded=%d/%d drift=%d\n' \
    "$installed" "${#labels[@]}" "$loaded" "${#labels[@]}" "$drift"
  [ "$installed" = "${#labels[@]}" ] && [ "$loaded" = "${#labels[@]}" ] && [ "$drift" = 0 ]
}

uninstall_runtime() {
  local label target
  "$launchctl_bin" bootout "$launch_domain/$keepawake_label" >/dev/null 2>&1 || true
  for label in "${labels[@]}"; do
    target="$user_launch_dir/$label.plist"
    if [ -f "$target" ] && [ ! -L "$target" ]; then
      find "$target" -maxdepth 0 -type f -delete
    fi
  done
  echo 'Removed Qiu Market login recovery and keep-awake registration; live services and logs were preserved.'
}

require_test_override_safety
case "$action" in
  install) install_runtime ;;
  status) status_runtime ;;
  uninstall) uninstall_runtime ;;
  *) echo "Usage: $0 install|status|uninstall" >&2; exit 2 ;;
esac
