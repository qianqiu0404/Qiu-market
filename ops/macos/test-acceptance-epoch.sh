#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_bin="$repo_root/ops/macos/fixtures/acceptance-epoch-bin"
fixture_env="$repo_root/ops/macos/fixtures/acceptance-epoch.env"
fixture_dir="$(mktemp -d /tmp/qiu-market-epoch-test.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

export PATH="$fixture_bin:$PATH"
export QIU_MARKET_OBSERVATION_DIR="$fixture_dir"
export QIU_MARKET_ACCEPTANCE_EPOCH_FILE="$fixture_dir/acceptance-epoch.json"
export QIU_MARKET_DATABASE_ENV_FILE="$fixture_env"
manager="$repo_root/ops/macos/manage-acceptance-epoch.sh"
deployment_id="dpl_FixtureRelease123"
deployment_url="https://qiu-market-fixture-release.vercel.app"
production_origin="https://qiu-market.vercel.app"
release_commit="19928325f9a1104d1dd3505a004dffb9fe52a714"

if "$manager" start \
  --deployment-id "$deployment_id" \
  --deployment-url "$production_origin" \
  --commit "$release_commit" \
  --epoch-id qiu-market-invalid-alias \
  --production-origin "$production_origin" >/dev/null 2>&1; then
  echo "Acceptance epoch incorrectly allowed a mutable Production alias." >&2
  exit 1
fi

"$manager" start \
  --deployment-id "$deployment_id" \
  --deployment-url "$deployment_url" \
  --commit "$release_commit" \
  --epoch-id qiu-market-fixture-first \
  --production-origin "$production_origin" >/dev/null

jq -e '
  . as $root |
  .schema_version == 2 and
  .epoch_id == "qiu-market-fixture-first" and
  .status == "active" and
  .deployment_id == "dpl_FixtureRelease123" and
  (.dex_canaries | keys | sort) == ["pancakeswap", "uniswap"] and
  all(.dex_canaries[];
    .selected_at == $root.started_at and
    .selected_max_gap_seconds <= 600
  )
' "$fixture_dir/acceptance-epoch.json" >/dev/null

if "$manager" start \
  --deployment-id "$deployment_id" \
  --deployment-url "$deployment_url" \
  --commit "$release_commit" \
  --epoch-id qiu-market-fixture-overwrite \
  --production-origin "$production_origin" >/dev/null 2>&1; then
  echo "Acceptance epoch incorrectly overwrote an active epoch." >&2
  exit 1
fi

"$manager" stop >/dev/null
jq -e '
  .status == "stopped" and
  (.stopped_at | type == "string")
' "$fixture_dir/acceptance-epoch.json" >/dev/null

"$manager" start \
  --deployment-id "$deployment_id" \
  --deployment-url "$deployment_url" \
  --commit "$release_commit" \
  --epoch-id qiu-market-fixture-second \
  --production-origin "$production_origin" >/dev/null

jq -e '
  .epoch_id == "qiu-market-fixture-second" and
  .status == "active"
' "$fixture_dir/acceptance-epoch.json" >/dev/null
archive_count="$(
  find "$fixture_dir/archive" -type f \
    -name 'acceptance-epoch-qiu-market-fixture-first-*.json' |
    wc -l |
    tr -d ' '
)"
if [ "$archive_count" != 1 ]; then
  echo "Stopped acceptance epoch was not archived exactly once." >&2
  exit 1
fi

"$manager" stop >/dev/null
echo "Qiu Market acceptance epoch lifecycle fixtures passed."
