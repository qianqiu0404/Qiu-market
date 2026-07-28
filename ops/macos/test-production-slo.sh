#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture_dir="$(mktemp -d /tmp/qiu-market-slo-test.XXXXXX)"
cleanup() {
  find "$fixture_dir" -depth -delete
}
trap cleanup EXIT

window_end="$(date -u '+%s')"
window_end=$((window_end - window_end % 60))
window_start=$((window_end - 7 * 24 * 60 * 60 + 60))
healthy="$fixture_dir/healthy.jsonl"
with_gap="$fixture_dir/with-gap.jsonl"

jq -nc \
  --argjson start "$window_start" \
  --argjson end "$window_end" '
  range($start; $end + 1; 60) as $at |
  {
    observed_at: ($at | todateiso8601),
    current_checks_status: "passed",
    checks: {
      trading_bff_http: 200,
      system_bff_http: 200,
      uniswap_bff_http: 200,
      pancakeswap_bff_http: 200,
      disk_free_bytes: (30 * 1024 * 1024 * 1024)
    },
    latency_ms: {
      trading_bff: 100,
      system_bff: 100,
      uniswap_bff: 100,
      pancakeswap_bff: 100
    }
  }
' > "$healthy"

healthy_report="$(
  QIU_MARKET_SOAK_HISTORY="$healthy" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "production-recommendation" and
  .observed_minutes == 10080 and
  .missing_minutes == 0 and
  .availability_percent == 100
' <<<"$healthy_report" >/dev/null

awk 'NR <= 300 || NR > 360' "$healthy" > "$with_gap"
gap_report="$(
  QIU_MARKET_SOAK_HISTORY="$with_gap" \
    "$repo_root/ops/macos/summarize-production-slo.sh"
)"
jq -e '
  .status == "failed" and
  .missing_minutes == 60 and
  .availability_percent < 99.5 and
  .longest_observed_failure_seconds >= 3600
' <<<"$gap_report" >/dev/null

echo "Qiu Market production SLO fixtures passed."
