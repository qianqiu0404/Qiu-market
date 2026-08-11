#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d /tmp/qiu-market-log-rotation.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

runtime_root="$fixture_dir/d1-candidate"
log_dir="$runtime_root/logs"
launch_dir="$runtime_root/launchd-live"
fixture_bin="$fixture_dir/bin"
launch_log="$fixture_dir/launchctl.log"
mkdir -p "$runtime_root/ops" "$runtime_root/run" "$runtime_root/evidence" \
  "$log_dir" "$launch_dir" "$fixture_bin"

dd if=/dev/zero of="$log_dir/live-crawler.err.log" bs=220 count=1 2>/dev/null
printf 'small\n' > "$log_dir/live-worker.out.log"
dd if=/dev/zero of="$runtime_root/evidence/live-provider-secret.log" bs=220 count=1 2>/dev/null
chmod 644 "$log_dir/live-crawler.err.log" "$log_dir/live-worker.out.log" \
  "$runtime_root/evidence/live-provider-secret.log"
tail -c 40 "$log_dir/live-crawler.err.log" > "$fixture_dir/expected-retained"

QIU_MARKET_LIVE_RUNTIME_ROOT="$runtime_root" \
QIU_MARKET_LOG_MAX_BYTES=100 \
QIU_MARKET_LOG_KEEP_BYTES=40 \
QIU_MARKET_LOG_ARCHIVE_GENERATIONS=2 \
  "$repo_root/ops/macos/rotate-live-logs.sh"

test "$(stat -f '%z' "$log_dir/live-crawler.err.log")" = 40
test "$(stat -f '%z' "$log_dir/live-crawler.err.log.1")" = 100
cmp "$fixture_dir/expected-retained" "$log_dir/live-crawler.err.log"
test "$(stat -f '%Lp' "$log_dir/live-crawler.err.log")" = 600
test "$(stat -f '%Lp' "$log_dir/live-crawler.err.log.1")" = 600
test "$(stat -f '%Lp' "$log_dir/live-worker.out.log")" = 600
test "$(stat -f '%z' "$runtime_root/evidence/live-provider-secret.log")" = 220
test "$(stat -f '%Lp' "$runtime_root/evidence/live-provider-secret.log")" = 644

dd if=/dev/zero bs=120 count=1 2>/dev/null >> "$log_dir/live-crawler.err.log"
QIU_MARKET_LIVE_RUNTIME_ROOT="$runtime_root" \
QIU_MARKET_LOG_MAX_BYTES=100 \
QIU_MARKET_LOG_KEEP_BYTES=40 \
QIU_MARKET_LOG_ARCHIVE_GENERATIONS=2 \
  "$repo_root/ops/macos/rotate-live-logs.sh"
test "$(stat -f '%z' "$log_dir/live-crawler.err.log")" = 40
test "$(stat -f '%z' "$log_dir/live-crawler.err.log.1")" = 100
test "$(stat -f '%z' "$log_dir/live-crawler.err.log.2")" = 100
test ! -e "$log_dir/live-crawler.err.log.3"

if QIU_MARKET_LIVE_RUNTIME_ROOT="$runtime_root" \
  QIU_MARKET_LOG_MAX_BYTES=100 \
  QIU_MARKET_LOG_KEEP_BYTES=100 \
  "$repo_root/ops/macos/rotate-live-logs.sh" >/dev/null 2>&1; then
  echo "Invalid log rotation bounds were accepted." >&2
  exit 1
fi

cat > "$fixture_bin/launchctl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$QIU_LOG_ROTATION_LAUNCH_LOG"
exit 0
SH
cat > "$fixture_bin/plutil" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod 700 "$fixture_bin/launchctl" "$fixture_bin/plutil"

export PATH="$fixture_bin:/usr/bin:/bin"
export QIU_LOG_ROTATION_LAUNCH_LOG="$launch_log"
export QIU_MARKET_LIVE_RUNTIME_ROOT="$runtime_root"
export QIU_MARKET_LIVE_LAUNCH_DIR="$launch_dir"
export QIU_MARKET_LIVE_LAUNCH_DOMAIN="gui/fixture"

"$repo_root/ops/macos/manage-live-log-rotation.sh" install >/dev/null
plist="$launch_dir/com.qiu-market.live.log-rotation.plist"
test -f "$plist"
test "$(stat -f '%Lp' "$plist")" = 600
test -x "$runtime_root/ops/live-log-rotation"
test "$(stat -f '%Lp' "$runtime_root/ops/live-log-rotation")" = 700
grep -F "$runtime_root/ops/live-log-rotation" "$plist" >/dev/null
grep -F '<string>com.qiu-market.live.log-rotation</string>' "$plist" >/dev/null
grep -F 'bootstrap gui/fixture' "$launch_log" >/dev/null

"$repo_root/ops/macos/manage-live-log-rotation.sh" status |
  grep -Fx 'log_rotation_status=managed' >/dev/null
"$repo_root/ops/macos/manage-live-log-rotation.sh" uninstall >/dev/null
test ! -e "$plist"
test ! -e "$runtime_root/ops/live-log-rotation"
test -f "$log_dir/live-crawler.err.log"

echo "Qiu Market bounded live-log rotation fixtures passed."
