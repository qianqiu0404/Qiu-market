#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d /tmp/qiu-market-guardian-recovery.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

fixture_bin="$fixture_dir/bin"
support_dir="$fixture_dir/support"
launch_log="$fixture_dir/launchctl.log"
mkdir -p "$fixture_bin" "$support_dir"

cat > "$fixture_bin/curl" <<'SH'
#!/usr/bin/env bash
url="${*: -1}"
case "$url" in
  */healthz)
    printf 'ok\n'
    ;;
  */api/v1/trading/markets/BTC-USDT/status)
    printf '%s\n' '{"state":"ready","last_error":"","outbox_state":"ready","outbox_last_error":""}'
    ;;
  */api/v1/trading/recovery/status)
    if [ "${QIU_GUARDIAN_RECOVERY_FIXTURE:-blocked}" = writable ]; then
      printf '%s\n' '{"schema_version":2,"phase":"writable","writes_enabled":true,"continuity_uncertain":false,"last_error":""}'
    else
      printf '%s\n' '{"schema_version":2,"phase":"transport_warmup","writes_enabled":false,"continuity_uncertain":false,"last_error":""}'
    fi
    ;;
  *)
    exit 1
    ;;
esac
SH
cat > "$fixture_bin/launchctl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$QIU_GUARDIAN_LAUNCH_LOG"
if [ "${1:-}" = print ]; then
  exit 1
fi
exit 0
SH
cat > "$fixture_bin/nc" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat > "$fixture_bin/pg_isready" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat > "$fixture_bin/df" <<'SH'
#!/usr/bin/env bash
printf '%s\n' 'Filesystem 1024-blocks Used Available Capacity Mounted on'
printf '%s\n' '/dev/fixture 100000000 1 40000000 1% /System/Volumes/Data'
SH
chmod 700 "$fixture_bin"/*

cat > "$support_dir/database.env" <<'ENV'
MARKET_MASTER_DB_HOST=127.0.0.1
MARKET_MASTER_DB_PORT=5432
MARKET_MASTER_DB_NAME=qiu_fixture
ENV
cat > "$support_dir/production.env" <<'ENV'
MARKET_TRADING_RECOVERY_GATE_ENABLED=true
MARKET_FUNNEL_ORIGIN=https://fixture.invalid
ENV
chmod 600 "$support_dir/database.env" "$support_dir/production.env"

export PATH="$fixture_bin:/usr/bin:/bin"
export QIU_MARKET_SUPPORT_DIR="$support_dir"
export QIU_MARKET_ENV_FILE="$support_dir/production.env"
export QIU_MARKET_DATABASE_ENV_FILE="$support_dir/database.env"
export QIU_GUARDIAN_LAUNCH_LOG="$launch_log"

for _ in 1 2 3 4; do
  QIU_GUARDIAN_RECOVERY_FIXTURE=blocked \
    "$repo_root/ops/macos/guardian.sh"
done

if grep -F 'kickstart -k gui/' "$launch_log" | grep -F 'com.qiumarket.trading' >/dev/null; then
  echo "Guardian restarted trading even though recovery required operator proof." >&2
  exit 1
fi
grep -F 'operator proof is required and restart is intentionally suppressed' \
  "$support_dir/guardian/incidents.log" >/dev/null

QIU_GUARDIAN_RECOVERY_FIXTURE=writable \
  "$repo_root/ops/macos/guardian.sh"
if [ "$(cat "$support_dir/guardian/trading-failures")" != 0 ]; then
  echo "Writable recovery did not reset the trading failure count." >&2
  exit 1
fi

echo "Qiu Market guardian recovery fixtures passed."
