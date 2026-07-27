#!/usr/bin/env bash
set -euo pipefail

support_dir="$HOME/Library/Application Support/Qiu Market"
history_file="${QIU_MARKET_SOAK_HISTORY:-$support_dir/observations/production-soak.jsonl}"

if [ ! -s "$history_file" ]; then
  echo "No Qiu Market production soak samples are available." >&2
  exit 1
fi

cutoff="$(date -u -v-7d '+%Y-%m-%dT%H:%M:%SZ')"
jq -s --arg cutoff "$cutoff" '
  def p95($values):
    ($values | sort) as $sorted |
    if ($sorted | length) == 0 then null
    else $sorted[((($sorted | length) - 1) * 0.95 | floor)]
    end;
  map(select(.observed_at >= $cutoff)) as $samples |
  (reduce $samples[] as $sample (
    {failure_started: null, previous_at: null, longest_failure_seconds: 0};
    ($sample.observed_at | fromdateiso8601) as $at |
    if $sample.current_checks_status == "passed" then
      .failure_started = null | .previous_at = $at
    else
      .failure_started = (.failure_started // $at) |
      .previous_at = $at |
      .longest_failure_seconds = ([
        .longest_failure_seconds,
        ($at - .failure_started + 60)
      ] | max)
    end
  )) as $interruptions |
  ($samples | length) as $total |
  ([$samples[] | select(.current_checks_status == "passed")] | length) as $passed |
  ([$samples[] | select(
    (.checks.trading_bff_http // 0) >= 500 and
    (.checks.trading_bff_http // 0) < 600
  )] | length) as $rest5xx |
  ([$samples[].latency_ms.trading_bff | numbers]) as $restLatency |
  ([$samples[].checks.disk_free_bytes | numbers] | min // 0) as $minimumDisk |
  ($samples[0].observed_at // null) as $firstAt |
  ($samples[-1].observed_at // null) as $lastAt |
  {
    window: "rolling_7d",
    first_observed_at: $firstAt,
    last_observed_at: $lastAt,
    samples: $total,
    expected_samples: 10080,
    availability_percent: (
      if $total == 0 then 0 else (($passed * 10000 / $total | floor) / 100) end
    ),
    rest_5xx_percent: (
      if $total == 0 then 0 else (($rest5xx * 10000 / $total | floor) / 100) end
    ),
    rest_p95_ms: p95($restLatency),
    longest_observed_failure_seconds: $interruptions.longest_failure_seconds,
    minimum_disk_free_bytes: $minimumDisk,
    acceptance: {
      availability_at_least_99_5: (
        $total > 0 and (($passed / $total) >= 0.995)
      ),
      rest_5xx_below_0_5: (
        $total > 0 and (($rest5xx / $total) < 0.005)
      ),
      rest_p95_below_5s: (
        (p95($restLatency) // 20000) < 5000
      ),
      no_interruption_over_5m: (
        $interruptions.longest_failure_seconds <= 300
      ),
      disk_at_least_25gib: (
        $minimumDisk >= (25 * 1024 * 1024 * 1024)
      ),
      full_7d_sample_count: ($total >= 10080)
    }
  } |
  .status = (
    if .acceptance.full_7d_sample_count != true then "environment-pending"
    elif ([.acceptance[]] | all) then "production-recommendation"
    else "failed"
    end
  )
' "$history_file"
