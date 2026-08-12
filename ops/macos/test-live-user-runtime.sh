#!/usr/bin/env bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_root="$(mktemp -d /tmp/qiu-market-live-user-runtime.XXXXXX)"
runtime_root="$fixture_root/runtime"
user_launch_dir="$fixture_root/LaunchAgents"
fake_launchctl="$fixture_root/launchctl"
launch_state="$fixture_root/launch-state"
trap 'find "$fixture_root" -depth -delete' EXIT

mkdir -p "$runtime_root/launchd-r1" "$runtime_root/launchd-live" "$runtime_root/logs"

render_fixture_plist() {
  local label="$1" destination="$2" program="$3"
  sed -e "s|__LABEL__|$label|g" -e "s|__PROGRAM__|$program|g" > "$destination" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>__LABEL__</string>
<key>ProgramArguments</key><array><string>__PROGRAM__</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
</dict></plist>
PLIST
  chmod 600 "$destination"
}

render_fixture_plist com.qiu-market.d1r1.frontdoor "$runtime_root/launchd-r1/com.qiu-market.d1r1.frontdoor.plist" "$runtime_root/ops/frontdoor"
render_fixture_plist com.qiu-market.d1r1.stack "$runtime_root/launchd-r1/com.qiu-market.d1r1.stack.plist" "$runtime_root/ops/stack"
for role in crawler dex worker api-tunnel log-rotation; do
  render_fixture_plist "com.qiu-market.live.$role" "$runtime_root/launchd-live/com.qiu-market.live.$role.plist" "$runtime_root/ops/$role"
done

cat > "$fake_launchctl" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
state="${QIU_MARKET_LIVE_TEST_LAUNCH_STATE:?}"
case "$1" in
  print) grep -Fxq "$2" "$state" 2>/dev/null ;;
  bootstrap) printf '%s\n' "$2/com.qiu-market.live.keepawake" >> "$state" ;;
  bootout) grep -Fxv "$2" "$state" > "$state.next" 2>/dev/null || true; mv "$state.next" "$state" ;;
  *) exit 64 ;;
esac
SCRIPT
chmod 700 "$fake_launchctl"
for label in com.qiu-market.d1r1.frontdoor com.qiu-market.d1r1.stack \
  com.qiu-market.live.crawler com.qiu-market.live.dex com.qiu-market.live.worker \
  com.qiu-market.live.api-tunnel com.qiu-market.live.log-rotation; do
  printf 'gui/%s/%s\n' "$UID" "$label" >> "$launch_state"
done

export QIU_MARKET_LIVE_USER_RUNTIME_TEST_MODE=true
export QIU_MARKET_LIVE_RUNTIME_ROOT="$runtime_root"
export QIU_MARKET_LIVE_USER_LAUNCH_DIR="$user_launch_dir"
export QIU_MARKET_LIVE_LAUNCHCTL_BIN="$fake_launchctl"
export QIU_MARKET_LIVE_TEST_LAUNCH_STATE="$launch_state"

bash "$repo_root/ops/macos/manage-live-user-runtime.sh" install
bash "$repo_root/ops/macos/manage-live-user-runtime.sh" status
[ "$(find "$user_launch_dir" -maxdepth 1 -type f -name '*.plist' | wc -l | tr -d ' ')" = 8 ]
[ "$(stat -f '%Lp' "$user_launch_dir/com.qiu-market.live.keepawake.plist")" = 600 ]
[ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$user_launch_dir/com.qiu-market.live.keepawake.plist")" = /usr/bin/caffeinate ]
[ "$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$user_launch_dir/com.qiu-market.live.keepawake.plist")" = -i ]

printf '\n# drift\n' >> "$user_launch_dir/com.qiu-market.live.crawler.plist"
if bash "$repo_root/ops/macos/manage-live-user-runtime.sh" status; then
  echo 'status accepted a drifted login LaunchAgent' >&2
  exit 1
fi
bash "$repo_root/ops/macos/manage-live-user-runtime.sh" install
bash "$repo_root/ops/macos/manage-live-user-runtime.sh" uninstall
[ "$(find "$user_launch_dir" -maxdepth 1 -type f -name '*.plist' | wc -l | tr -d ' ')" = 0 ]

ln -s "$fixture_root/unsafe-target" "$user_launch_dir/com.qiu-market.live.crawler.plist"
if bash "$repo_root/ops/macos/manage-live-user-runtime.sh" install; then
  echo 'install accepted a symlinked login LaunchAgent target' >&2
  exit 1
fi
[ -L "$user_launch_dir/com.qiu-market.live.crawler.plist" ]

echo 'Qiu Market live user runtime fixture passed.'
