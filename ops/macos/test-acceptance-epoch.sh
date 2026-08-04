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
fixture_private_env="$fixture_dir/production.env"
cp "$fixture_env" "$fixture_private_env"
chmod 600 "$fixture_private_env"
export QIU_MARKET_DATABASE_ENV_FILE="$fixture_private_env"
export QIU_MARKET_TRANSPORT_SMOKE_FILE="$fixture_dir/transport-smoke.json"
runtime_target="$fixture_dir/runtime-release"
runtime_link="$fixture_dir/runtime-current"
mkdir -p "$runtime_target"
runtime_commit="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
printf 'git_commit=%s\n' "$runtime_commit" > "$runtime_target/runtime-manifest.env"
ln -s "$runtime_target" "$runtime_link"
export QIU_MARKET_RUNTIME_LINK="$runtime_link"
export QIU_MARKET_ACCEPTANCE_NOW_EPOCH=1785120000
manager="$repo_root/ops/macos/manage-acceptance-epoch.sh"
deployment_id="dpl_FixtureRelease123"
deployment_url="https://qiu-market-fixture-release.vercel.app"
production_origin="https://qiu-market.vercel.app"
release_commit="19928325f9a1104d1dd3505a004dffb9fe52a714"

jq -n \
  --arg deployment_id "$deployment_id" \
  --arg deployment_url "$deployment_url" \
  --arg deployment_commit "$release_commit" \
  --arg runtime_release_commit "$runtime_commit" '
  {
    schema_version: 1,
    status: "passed",
    deployment_id: $deployment_id,
    deployment_url: $deployment_url,
    deployment_commit: $deployment_commit,
    runtime_release_commit: $runtime_release_commit,
    completed_at: "2026-07-27T02:40:00Z",
    result: {
      status: "passed",
      acceptance: {
        full_30m_window: true,
        exactly_30_observed: true,
        all_minutes_passed: true,
        no_rest_5xx: true,
        rest_p95_below_5s: true,
        no_guardian_restart: true
      }
    }
  }
' > "$QIU_MARKET_TRANSPORT_SMOKE_FILE"

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
