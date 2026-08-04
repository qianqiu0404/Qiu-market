#!/usr/bin/env bash
set -euo pipefail
umask 077

action="${1:-status}"
support_dir="${QIU_MARKET_SUPPORT_DIR:-$HOME/Library/Application Support/Qiu Market}"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
history_file="${QIU_MARKET_SOAK_HISTORY:-$observation_dir/production-soak.jsonl}"
latest_file="${QIU_MARKET_OBSERVER_LATEST:-$observation_dir/latest.json}"
epoch_file="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$observation_dir/acceptance-epoch.json}"
smoke_file="${QIU_MARKET_TRANSPORT_SMOKE_FILE:-$observation_dir/transport-smoke.json}"
gate_file="${QIU_MARKET_PRODUCTION_GATE_FILE:-$support_dir/vercel-release/last.json}"
runtime_link="${QIU_MARKET_RUNTIME_LINK:-$support_dir/runtime-current}"
guardian_dir="${QIU_MARKET_GUARDIAN_DIR:-$support_dir/guardian}"

now_epoch() {
  printf '%s\n' "${QIU_MARKET_SMOKE_NOW_EPOCH:-$(date -u '+%s')}"
}

iso_at() {
  jq -nr --argjson epoch "$1" '$epoch | todateiso8601'
}

write_state() {
  local payload="$1"
  local temporary
  install -d -m 700 "$observation_dir"
  temporary="$(mktemp "$observation_dir/.transport-smoke.XXXXXX")"
  printf '%s\n' "$payload" > "$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$smoke_file"
}

runtime_commit() {
  local manifest="$runtime_link/runtime-manifest.env"
  [ -L "$runtime_link" ] && [ -f "$manifest" ] || return 1
  sed -n 's/^git_commit=//p' "$manifest" | head -1
}

evaluate() {
  if [ ! -s "$smoke_file" ]; then
    jq -n '{window:"transport_smoke_30m",status:"not-started"}'
    return
  fi
  if ! jq -e '
    .schema_version == 1 and
    (.status == "active" or .status == "passed" or .status == "failed") and
    (.deployment_id | type == "string" and startswith("dpl_")) and
    (.deployment_url | type == "string" and startswith("https://")) and
    (.deployment_commit | type == "string" and test("^[0-9a-f]{40}$")) and
    (.runtime_release_commit | type == "string" and test("^[0-9a-f]{40}$")) and
    (.network_interface | type == "string" and length > 0) and
    (.network_gateway | type == "string" and length > 0) and
    (.started_at | fromdateiso8601 | type == "number") and
    (.last_scheduled_at | fromdateiso8601 | type == "number")
  ' "$smoke_file" >/dev/null 2>&1; then
    echo "The transport smoke state is invalid." >&2
    return 1
  fi
  if jq -e '
    (.status == "passed" or .status == "failed") and
    (.result | type == "object")
  ' "$smoke_file" >/dev/null 2>&1; then
    jq '.result + {
      completed_at: .completed_at,
      terminal_state: .status,
      failure_reason: (.failure_reason // null)
    } | .status = .terminal_state' "$smoke_file"
    return
  fi

  local current_epoch
  local window_start
  local window_end
  local evaluation_end
  local expected
  local state
  local current_restart
  current_epoch="$(now_epoch)"
  [[ "$current_epoch" =~ ^[0-9]+$ ]] || {
    echo "QIU_MARKET_SMOKE_NOW_EPOCH must be a Unix timestamp." >&2
    return 1
  }
  current_epoch=$((current_epoch - current_epoch % 60))
  window_start="$(jq -r '.started_at | fromdateiso8601' "$smoke_file")"
  window_end="$(jq -r '.last_scheduled_at | fromdateiso8601' "$smoke_file")"
  evaluation_end="$current_epoch"
  if [ "$evaluation_end" -gt "$window_end" ]; then
    evaluation_end="$window_end"
  fi
  expected=0
  if [ "$evaluation_end" -ge "$window_start" ]; then
    expected=$(((evaluation_end - window_start) / 60 + 1))
  fi
  state="$(cat "$smoke_file")"
  current_restart="$(cat "$guardian_dir/last-automatic-restart-at" 2>/dev/null || echo 0)"
  [[ "$current_restart" =~ ^[0-9]+$ ]] || current_restart=0

  if [ ! -s "$history_file" ]; then
    jq -n \
      --argjson state "$state" \
      --argjson expected "$expected" \
      '{window:"transport_smoke_30m",status:"environment-pending",state:$state,expected_minutes:$expected,observed_minutes:0,missing_minutes:$expected}'
    return
  fi

  jq -s \
    --argjson state "$state" \
    --argjson window_start "$window_start" \
    --argjson window_end "$window_end" \
    --argjson evaluation_end "$evaluation_end" \
    --argjson expected "$expected" \
    --argjson current_restart "$current_restart" '
    def p95($values):
      ($values | sort) as $sorted |
      if ($sorted | length) == 0 then null
      else $sorted[((($sorted | length) - 1) * 0.95 | floor)] end;
    def matching:
      .schema_version == 4 and
      .deployment_id == $state.deployment_id and
      .deployment_url == $state.deployment_url and
      .deployment_commit == $state.deployment_commit and
      .runtime_release_commit == $state.runtime_release_commit and
      (.scheduled_at | type == "string") and
      ((.scheduled_at | fromdateiso8601) >= $window_start) and
      ((.scheduled_at | fromdateiso8601) <= $evaluation_end);
    def rest_probes:
      [
        {http:(.checks.trading_bff_http // 0),latency:(.latency_ms.trading_bff // null)},
        {http:(.checks.system_bff_http // 0),latency:(.latency_ms.system_bff // null)},
        {http:(.checks.uniswap_bff_http // 0),latency:(.latency_ms.uniswap_bff // null)},
        {http:(.checks.pancakeswap_bff_http // 0),latency:(.latency_ms.pancakeswap_bff // null)}
      ];
    ([.[] | select(matching)] | sort_by(.scheduled_at, .finished_at)) as $samples |
    ($samples | group_by(.scheduled_at) | map({
      scheduled_at: .[0].scheduled_at,
      passed: all(.[];
        .current_checks_status == "passed" and
        .failure_scope == "none" and
        .checks.tailscale_backend_state == "Running" and
        .checks.tailscale_health_ok == true and
        (.checks.tailscale_health | length) == 0 and
        .checks.guardian_last_automatic_restart_at == $state.guardian_restart_baseline and
        .checks.network_interface == $state.network_interface and
        .checks.network_gateway == $state.network_gateway
      )
    })) as $slots |
    (reduce $slots[] as $slot ({}; .[($slot.scheduled_at | fromdateiso8601 | tostring)] = $slot)) as $by_slot |
    ([range(0; $expected) as $offset |
      ($window_start + ($offset * 60)) as $at |
      ($by_slot[($at | tostring)] // null) as $slot |
      {at:$at,present:($slot != null),passed:($slot != null and $slot.passed)}
    ]) as $minutes |
    ([$samples[] | rest_probes[]]) as $rest |
    ([$rest[] | select(.http >= 500 and .http < 600)] | length) as $rest5xx |
    ([$rest[].latency | numbers]) as $latencies |
    ([$minutes[] | select(.present)] | length) as $observed |
    ([$minutes[] | select(.passed)] | length) as $passed |
    ($current_restart == $state.guardian_restart_baseline) as $restart_unchanged |
    {
      window:"transport_smoke_30m",
      smoke_id:$state.smoke_id,
      deployment_id:$state.deployment_id,
      deployment_url:$state.deployment_url,
      deployment_commit:$state.deployment_commit,
      runtime_release_commit:$state.runtime_release_commit,
      started_at:$state.started_at,
      last_scheduled_at:$state.last_scheduled_at,
      evaluated_through:(if $expected == 0 then null else ($evaluation_end | todateiso8601) end),
      expected_minutes:$expected,
      required_minutes:30,
      observed_minutes:$observed,
      passed_minutes:$passed,
      missing_minutes:($expected - $observed),
      rest_5xx_count:$rest5xx,
      rest_p95_ms:p95($latencies),
      guardian_restart_unchanged:$restart_unchanged,
      network_interface:$state.network_interface,
      network_gateway:$state.network_gateway,
      network_identity_unchanged:all($samples[];
        .checks.network_interface == $state.network_interface and
        .checks.network_gateway == $state.network_gateway
      ),
      acceptance:{
        full_30m_window:($evaluation_end >= $window_end),
        exactly_30_observed:($expected == 30 and $observed == 30),
        all_minutes_passed:($expected == 30 and $passed == 30),
        no_rest_5xx:($rest5xx == 0),
        rest_p95_below_5s:((p95($latencies) // 20000) < 5000),
        no_guardian_restart:$restart_unchanged,
        network_identity_unchanged:all($samples[];
          .checks.network_interface == $state.network_interface and
          .checks.network_gateway == $state.network_gateway
        )
      }
    } |
    .status = (
      if .acceptance.full_30m_window != true then "environment-pending"
      elif ([.acceptance[]] | all) then "passed"
      else "failed" end
    )
  ' "$history_file"
}

case "$action" in
  start)
    command -v jq >/dev/null 2>&1 || {
      echo "jq is required." >&2
      exit 1
    }
    if [ -s "$epoch_file" ] && [ "$(jq -r '.status // ""' "$epoch_file")" = active ]; then
      echo "Stop the active seven-day acceptance epoch before transport smoke." >&2
      exit 1
    fi
    if [ -s "$smoke_file" ] && [ "$(jq -r '.status // ""' "$smoke_file")" = active ]; then
      echo "A transport smoke window is already active." >&2
      exit 1
    fi
    if [ -e "$guardian_dir/funnel-restart-at" ] || [ -e "$guardian_dir/trading-restart-at" ]; then
      echo "Guardian restart budget has not completed its 15-minute stable reset." >&2
      exit 1
    fi
    observed_runtime="$(runtime_commit || true)"
    [[ "$observed_runtime" =~ ^[0-9a-f]{40}$ ]] || {
      echo "The immutable runtime release is unavailable." >&2
      exit 1
    }
    if ! jq -e '
      .status == "production-gate-passed" and
      (.candidate.commit | test("^[0-9a-f]{40}$")) and
      (.promoted.id | startswith("dpl_")) and
      (.promoted.url | startswith("https://"))
    ' "$gate_file" >/dev/null 2>&1; then
      echo "The production release gate evidence is unavailable or invalid." >&2
      exit 1
    fi
    deployment_id="$(jq -r '.promoted.id' "$gate_file")"
    deployment_url="$(jq -r '.promoted.url' "$gate_file")"
    deployment_commit="$(jq -r '.candidate.commit' "$gate_file")"
    current_epoch="$(now_epoch)"
    latest_epoch="$(jq -r '.scheduled_at | fromdateiso8601' "$latest_file" 2>/dev/null || echo 0)"
    guardian_baseline="$(cat "$guardian_dir/last-automatic-restart-at" 2>/dev/null || echo 0)"
    [[ "$guardian_baseline" =~ ^[0-9]+$ ]] || guardian_baseline=0
    if ! jq -e \
      --arg id "$deployment_id" \
      --arg url "$deployment_url" \
      --arg commit "$deployment_commit" \
      --arg runtime "$observed_runtime" \
      --argjson guardian "$guardian_baseline" '
        .schema_version == 4 and
        .deployment_id == $id and
        .deployment_url == $url and
        .deployment_commit == $commit and
        .runtime_release_commit == $runtime and
        .current_checks_status == "passed" and
        .failure_scope == "none" and
        .checks.tailscale_backend_state == "Running" and
        .checks.tailscale_health_ok == true and
        (.checks.tailscale_health | length) == 0 and
        .checks.guardian_last_automatic_restart_at == $guardian and
        (.checks.network_interface | type == "string" and length > 0) and
        (.checks.network_gateway | type == "string" and length > 0)
      ' "$latest_file" >/dev/null 2>&1 ||
      [ "$((current_epoch - latest_epoch))" -gt 180 ]; then
      echo "A fresh passing observer sample for the exact production and runtime release is required." >&2
      exit 1
    fi

    install -d -m 700 "$observation_dir/archive"
    if [ -s "$smoke_file" ]; then
      archive_id="$(jq -r '.smoke_id // "unknown"' "$smoke_file" | tr -cd 'A-Za-z0-9._-')"
      archive_target="$observation_dir/archive/transport-smoke-$archive_id-$(date -u '+%Y%m%dT%H%M%SZ').json"
      [ ! -e "$archive_target" ] || {
        echo "Refusing to overwrite transport smoke archive." >&2
        exit 1
      }
      cp "$smoke_file" "$archive_target"
      chmod 600 "$archive_target"
    fi
    started_epoch=$(((current_epoch / 60 + 1) * 60))
    last_epoch=$((started_epoch + 29 * 60))
    network_interface="$(jq -r '.checks.network_interface' "$latest_file")"
    network_gateway="$(jq -r '.checks.network_gateway' "$latest_file")"
    payload="$(jq -n \
      --arg smoke_id "qiu-market-transport-$(date -u '+%Y%m%dT%H%M%SZ')-$RANDOM" \
      --arg deployment_id "$deployment_id" \
      --arg deployment_url "$deployment_url" \
      --arg deployment_commit "$deployment_commit" \
      --arg runtime_release_commit "$observed_runtime" \
      --arg network_interface "$network_interface" \
      --arg network_gateway "$network_gateway" \
      --arg created_at "$(iso_at "$current_epoch")" \
      --arg started_at "$(iso_at "$started_epoch")" \
      --arg last_scheduled_at "$(iso_at "$last_epoch")" \
      --argjson guardian_restart_baseline "$guardian_baseline" '
        {
          schema_version:1,
          smoke_id:$smoke_id,
          status:"active",
          deployment_id:$deployment_id,
          deployment_url:$deployment_url,
          deployment_commit:$deployment_commit,
          runtime_release_commit:$runtime_release_commit,
          network_interface:$network_interface,
          network_gateway:$network_gateway,
          guardian_restart_baseline:$guardian_restart_baseline,
          created_at:$created_at,
          started_at:$started_at,
          last_scheduled_at:$last_scheduled_at,
          completed_at:null,
          result:null
        }
      ')"
    write_state "$payload"
    echo "Transport smoke starts at $(iso_at "$started_epoch") and requires 30 exact minute slots."
    jq . "$smoke_file"
    ;;
  status)
    evaluate
    ;;
  finish)
    report="$(evaluate)"
    if [ "$(jq -r '.status' <<<"$report")" = environment-pending ]; then
      echo "$report"
      echo "The 30-minute transport smoke window has not elapsed." >&2
      exit 1
    fi
    completed_at="$(iso_at "$(now_epoch)")"
    final_status="$(jq -r '.status' <<<"$report")"
    payload="$(jq \
      --arg status "$final_status" \
      --arg completed_at "$completed_at" \
      --argjson result "$report" '
        .status = $status |
        .completed_at = $completed_at |
        .result = $result
      ' "$smoke_file")"
    write_state "$payload"
    echo "$report"
    [ "$final_status" = passed ] || exit 1
    ;;
  abort)
    [ -s "$smoke_file" ] || {
      echo "No transport smoke window exists." >&2
      exit 1
    }
    [ "$(jq -r '.status // ""' "$smoke_file")" = active ] || {
      echo "Transport smoke window is not active." >&2
      exit 1
    }
    if ! report="$(evaluate 2>/dev/null)"; then
      report="$(jq -n --argjson state "$(cat "$smoke_file")" '{
        window:"transport_smoke_30m",
        smoke_id:($state.smoke_id // "unknown"),
        status:"failed",
        reason:"state_schema_changed_during_active_window"
      }')"
    fi
    completed_at="$(iso_at "$(now_epoch)")"
    payload="$(jq \
      --arg completed_at "$completed_at" \
      --argjson result "$report" '
        .status = "failed" |
        .completed_at = $completed_at |
        .failure_reason = "operator_aborted_after_irreversible_gate_failure" |
        .result = ($result | .status = "failed")
      ' "$smoke_file")"
    write_state "$payload"
    jq . "$smoke_file"
    ;;
  *)
    echo "Usage: $0 start|status|finish|abort" >&2
    exit 2
    ;;
esac
