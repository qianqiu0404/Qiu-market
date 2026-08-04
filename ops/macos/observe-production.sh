#!/usr/bin/env bash
set -euo pipefail

# LaunchAgents inherit only the system PATH. PostgreSQL is installed through
# Homebrew on this Mac mini, so make the dependency lookup deterministic while
# retaining the caller's existing PATH for interactive runs.
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
support_dir="$HOME/Library/Application Support/Qiu Market"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
production_env="${QIU_MARKET_ENV_FILE:-$support_dir/production.env}"
database_env="${QIU_MARKET_DATABASE_ENV_FILE:-$support_dir/database.env}"
epoch_file="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$observation_dir/acceptance-epoch.json}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"
funnel_origin="${QIU_MARKET_FUNNEL_ORIGIN:-https://xiuqiudemac-mini.tail2e4386.ts.net}"
tailscale_socket="$support_dir/tailscale/tailscaled.sock"
tailscale_cli="/opt/homebrew/bin/tailscale"
runtime_link="$support_dir/runtime-current"
guardian_last_restart_file="$support_dir/guardian/last-automatic-restart-at"
lock_dir="$observation_dir/.observer.lock"
started_epoch="$(date -u '+%s')"
scheduled_epoch=$((started_epoch - started_epoch % 60))

# shellcheck disable=SC1091
source "$repo_root/ops/macos/proxy-env.sh"
qiu_export_system_proxy

mkdir -p "$observation_dir"
if ! mkdir "$lock_dir" 2>/dev/null; then
  echo "Qiu Market production observation is already running."
  exit 0
fi

temp_dir="$(mktemp -d "$observation_dir/.run.XXXXXX")"
cleanup() {
  rm -rf -- "$temp_dir"
  rmdir "$lock_dir" 2>/dev/null || true
}
trap cleanup EXIT

for command in curl jq psql; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required observer dependency is unavailable: $command" >&2
    exit 1
  fi
done
started_at="$(jq -nr --argjson epoch "$started_epoch" '$epoch | todateiso8601')"
scheduled_at="$(jq -nr --argjson epoch "$scheduled_epoch" '$epoch | todateiso8601')"
if [ ! -f "$database_env" ] || [ ! -f "$production_env" ]; then
  echo "Qiu Market private database or production environment is unavailable." >&2
  exit 1
fi

# shellcheck disable=SC1091
source "$database_env"
# shellcheck disable=SC1091
source "$production_env"

curl_code() {
  local output="$1"
  shift
  local result
  local code
  local duration
  result="$(curl --silent --show-error --max-time 20 \
    --dump-header "$output.headers" \
    --output "$output" --write-out '%{http_code} %{time_total}' "$@" \
    2>"$output.error" || true)"
  code="${result%% *}"
  duration="${result#* }"
  if [[ "$duration" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    awk -v seconds="$duration" 'BEGIN { printf "%.0f\n", seconds * 1000 }' \
      > "$output.latency-ms"
  else
    printf '20000\n' > "$output.latency-ms"
  fi
  if [[ "$code" =~ ^[0-9]{3}$ ]]; then
    printf '%s' "$code"
  else
    printf '000'
  fi
}

error_summary() {
  local error_file="$1"
  if [ ! -s "$error_file" ]; then
    return 0
  fi
  tr '\r\n\t' '   ' < "$error_file" |
    sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//' |
    cut -c 1-512
}

header_value() {
  local header_file="$1"
  local header_name="$2"
  awk -v name="$header_name" '
    BEGIN { IGNORECASE = 1 }
    {
      sub(/\r$/, "")
      split($0, parts, ":")
      if (tolower(parts[1]) == tolower(name)) {
        sub(/^[^:]*:[[:space:]]*/, "", $0)
        value = $0
      }
    }
    END { print value }
  ' "$header_file"
}

dashboard_body() {
  local venue="$1"
  jq -nc --arg venue "$venue" '{
    consumer_token: "production-observer",
    page: 1,
    page_size: 100,
    venue: $venue,
    filter: "assets",
    sort_by: "rank",
    sort_direction: "asc",
    include_uncovered: true,
    universe: "provider_top50"
  }'
}

probe_pids=()
curl_code "$temp_dir/site.html" "$production_origin/markets" \
  > "$temp_dir/site.http" &
probe_pids+=("$!")
curl_code "$temp_dir/funnel-health.txt" "$funnel_origin/healthz" \
  > "$temp_dir/funnel-health.http" &
probe_pids+=("$!")
curl_code "$temp_dir/unsigned.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data '{"consumer_token":"production-observer","venue":"all","universe":"provider_union"}' \
  "$funnel_origin/api/v2/get_market_overview" \
  > "$temp_dir/unsigned.http" &
probe_pids+=("$!")
curl_code "$temp_dir/trading.json" \
  "$production_origin/api/v1/trading/markets/BTC-USDT/status" \
  > "$temp_dir/trading.http" &
probe_pids+=("$!")
curl_code "$temp_dir/system.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data '{"consumer_token":"production-observer"}' \
  "$production_origin/api/v1/get_system_overview" \
  > "$temp_dir/system.http" &
probe_pids+=("$!")
curl_code "$temp_dir/uniswap.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data "$(dashboard_body uniswap)" \
  "$production_origin/api/v2/get_asset_dashboard" \
  > "$temp_dir/uniswap.http" &
probe_pids+=("$!")
curl_code "$temp_dir/pancakeswap.json" \
  --request POST \
  --header 'content-type: application/json' \
  --data "$(dashboard_body pancakeswap)" \
  "$production_origin/api/v2/get_asset_dashboard" \
  > "$temp_dir/pancakeswap.http" &
probe_pids+=("$!")

for probe_pid in "${probe_pids[@]}"; do
  wait "$probe_pid"
done

site_http="$(cat "$temp_dir/site.http")"
funnel_health_http="$(cat "$temp_dir/funnel-health.http")"
unsigned_http="$(cat "$temp_dir/unsigned.http")"
trading_http="$(cat "$temp_dir/trading.http")"
system_http="$(cat "$temp_dir/system.http")"
uniswap_http="$(cat "$temp_dir/uniswap.http")"
pancake_http="$(cat "$temp_dir/pancakeswap.http")"

tailscale_backend_state="unavailable"
tailscale_health_json='["tailscale status unavailable"]'
tailscale_health_ok=false
if [ -x "$tailscale_cli" ] && [ -S "$tailscale_socket" ]; then
  tailscale_status_json="$(
    "$tailscale_cli" --socket="$tailscale_socket" status --json 2>/dev/null || echo '{}'
  )"
  tailscale_backend_state="$(
    jq -r '.BackendState // "unavailable"' <<<"$tailscale_status_json" 2>/dev/null || echo unavailable
  )"
  tailscale_health_json="$(
    jq -c 'if (.Health | type) == "array" then .Health else [] end' \
      <<<"$tailscale_status_json" 2>/dev/null || echo '["invalid tailscale health payload"]'
  )"
  if [ "$tailscale_backend_state" = Running ] &&
    jq -e 'length == 0' <<<"$tailscale_health_json" >/dev/null 2>&1; then
    tailscale_health_ok=true
  fi
fi

runtime_release_commit="unmanaged"
if [ -L "$runtime_link" ] && [ -f "$runtime_link/runtime-manifest.env" ]; then
  runtime_release_commit="$(
    sed -n 's/^git_commit=//p' "$runtime_link/runtime-manifest.env" | head -1
  )"
fi
guardian_last_automatic_restart_at="$(
  cat "$guardian_last_restart_file" 2>/dev/null || echo 0
)"
if [[ ! "$guardian_last_automatic_restart_at" =~ ^[0-9]+$ ]]; then
  guardian_last_automatic_restart_at=0
fi

network_route="$(route -n get default 2>/dev/null || true)"
network_interface="$(
  awk '$1 == "interface:" { print $2; exit }' <<<"$network_route"
)"
network_gateway="$(
  awk '$1 == "gateway:" { print $2; exit }' <<<"$network_route"
)"
network_ipv4=""
if [ -n "$network_interface" ]; then
  network_ipv4="$(ipconfig getifaddr "$network_interface" 2>/dev/null || true)"
fi

trading_provenance="$(header_value "$temp_dir/trading.json.headers" X-Qiu-Market-Provenance)"
trading_release_commit="$(header_value "$temp_dir/trading.json.headers" X-Qiu-Market-Release-Commit)"
trading_deployment_id="$(header_value "$temp_dir/trading.json.headers" X-Qiu-Market-Deployment-ID)"
trading_deployment_url="$(header_value "$temp_dir/trading.json.headers" X-Qiu-Market-Deployment-URL)"
system_provenance="$(header_value "$temp_dir/system.json.headers" X-Qiu-Market-Provenance)"
system_release_commit="$(header_value "$temp_dir/system.json.headers" X-Qiu-Market-Release-Commit)"
system_deployment_id="$(header_value "$temp_dir/system.json.headers" X-Qiu-Market-Deployment-ID)"
system_deployment_url="$(header_value "$temp_dir/system.json.headers" X-Qiu-Market-Deployment-URL)"
uniswap_provenance="$(header_value "$temp_dir/uniswap.json.headers" X-Qiu-Market-Provenance)"
uniswap_release_commit="$(header_value "$temp_dir/uniswap.json.headers" X-Qiu-Market-Release-Commit)"
uniswap_deployment_id="$(header_value "$temp_dir/uniswap.json.headers" X-Qiu-Market-Deployment-ID)"
uniswap_deployment_url="$(header_value "$temp_dir/uniswap.json.headers" X-Qiu-Market-Deployment-URL)"
pancake_provenance="$(header_value "$temp_dir/pancakeswap.json.headers" X-Qiu-Market-Provenance)"
pancake_release_commit="$(header_value "$temp_dir/pancakeswap.json.headers" X-Qiu-Market-Release-Commit)"
pancake_deployment_id="$(header_value "$temp_dir/pancakeswap.json.headers" X-Qiu-Market-Deployment-ID)"
pancake_deployment_url="$(header_value "$temp_dir/pancakeswap.json.headers" X-Qiu-Market-Deployment-URL)"

acceptance_epoch_id=""
expected_deployment_id=""
expected_deployment_url=""
expected_deployment_commit=""
epoch_started_at=""
epoch_canaries="{}"
epoch_active=false
if [ -s "$epoch_file" ] &&
  jq -e '
    . as $root |
    .schema_version == 2 and
    .status == "active" and
    (.epoch_id | type == "string" and length >= 8) and
    (.deployment_id | type == "string" and startswith("dpl_")) and
    (.deployment_url | type == "string" and startswith("https://")) and
    (.deployment_commit | type == "string" and test("^[0-9a-f]{40}$")) and
    (.started_at | type == "string") and
    (.started_at | try fromdateiso8601 catch null | type == "number") and
    (.dex_canaries | keys | sort) == ["pancakeswap", "uniswap"] and
    all(.dex_canaries[];
      (.asset_guid | type == "string") and
      (.route_key | type == "string" and length > 0) and
      (.quote_notional_usd | type == "string") and
      (.selected_at == $root.started_at)
    )
  ' "$epoch_file" >/dev/null 2>&1; then
  acceptance_epoch_id="$(jq -r '.epoch_id' "$epoch_file")"
  expected_deployment_id="$(jq -r '.deployment_id' "$epoch_file")"
  expected_deployment_url="$(jq -r '.deployment_url' "$epoch_file")"
  expected_deployment_commit="$(jq -r '.deployment_commit' "$epoch_file")"
  epoch_started_at="$(jq -r '.started_at' "$epoch_file")"
  epoch_canaries="$(jq -c '.dex_canaries' "$epoch_file")"
  if [ "$scheduled_epoch" -ge "$(jq -r '.started_at | fromdateiso8601' "$epoch_file")" ]; then
    epoch_active=true
  fi
fi

release_provenance_match=false
if [ "$epoch_active" = true ] &&
  [ "$(jq -r '.production_origin' "$epoch_file")" = "$production_origin" ] &&
  [ "$trading_provenance" = VERIFIED ] &&
  [ "$system_provenance" = VERIFIED ] &&
  [ "$uniswap_provenance" = VERIFIED ] &&
  [ "$pancake_provenance" = VERIFIED ] &&
  [ "$trading_release_commit" = "$expected_deployment_commit" ] &&
  [ "$system_release_commit" = "$expected_deployment_commit" ] &&
  [ "$uniswap_release_commit" = "$expected_deployment_commit" ] &&
  [ "$pancake_release_commit" = "$expected_deployment_commit" ] &&
  [ "$trading_deployment_id" = "$expected_deployment_id" ] &&
  [ "$system_deployment_id" = "$expected_deployment_id" ] &&
  [ "$uniswap_deployment_id" = "$expected_deployment_id" ] &&
  [ "$pancake_deployment_id" = "$expected_deployment_id" ] &&
  [ "${trading_deployment_url%/}" = "$expected_deployment_url" ] &&
  [ "${system_deployment_url%/}" = "$expected_deployment_url" ] &&
  [ "${uniswap_deployment_url%/}" = "$expected_deployment_url" ] &&
  [ "${pancake_deployment_url%/}" = "$expected_deployment_url" ]; then
  release_provenance_match=true
fi

dex_summary() {
  local provider="$1"
  local http_code="$2"
  local source_file="$3"
  if [ "$http_code" = 200 ] && jq -e '.code == 2000 and (.result | type == "array")' \
    "$source_file" >/dev/null 2>&1; then
    jq -c --arg provider "$provider" '{
      provider: $provider,
      http_status: 200,
      total: (.total // (.result | length)),
      displayed_assets: ([.result[] | select(.display_available == true)] | length),
      current_route_assets: ([.result[] | select(.dex_route_available == true)] | length),
      reference_only_assets: ([
        .result[] |
        select(.coverage_status == "reference_only")
      ] | length),
      unavailable_assets: ([
        .result[] |
        select(.display_available != true)
      ] | length)
    }' "$source_file"
  else
    jq -nc --arg provider "$provider" --arg http "$http_code" '{
      provider: $provider,
      http_status: ($http | tonumber? // 0),
      total: 0,
      displayed_assets: 0,
      current_route_assets: 0,
      reference_only_assets: 0,
      unavailable_assets: 0
    }'
  fi
}

uniswap_summary="$(dex_summary uniswap "$uniswap_http" "$temp_dir/uniswap.json")"
pancake_summary="$(dex_summary pancakeswap "$pancake_http" "$temp_dir/pancakeswap.json")"

trading_state=""
trading_sequence=""
trading_last_error=""
trading_outbox_state=""
trading_outbox_last_error=""
if [ "$trading_http" = 200 ]; then
  trading_state="$(jq -r '.state // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_sequence="$(jq -r '.sequence // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_last_error="$(jq -r '.last_error // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_outbox_state="$(jq -r '.outbox_state // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
  trading_outbox_last_error="$(jq -r '.outbox_last_error // ""' "$temp_dir/trading.json" 2>/dev/null || true)"
fi

disk_free_bytes=0
disk_state=""
retention_last_success_at=0
retention_last_error=""
if [ "$system_http" = 200 ]; then
  disk_free_bytes="$(jq -r '.result.storage.disk_free_bytes // 0' "$temp_dir/system.json" 2>/dev/null || echo 0)"
  disk_state="$(jq -r '.result.storage.disk_state // ""' "$temp_dir/system.json" 2>/dev/null || true)"
  retention_last_success_at="$(jq -r '.result.storage.retention_last_success_at // 0' "$temp_dir/system.json" 2>/dev/null || echo 0)"
  retention_last_error="$(jq -r '.result.storage.retention_last_error // ""' "$temp_dir/system.json" 2>/dev/null || true)"
fi

psql_command=(
  psql -X -v ON_ERROR_STOP=1
  -h "$MARKET_MASTER_DB_HOST"
  -p "$MARKET_MASTER_DB_PORT"
  -U "$MARKET_MASTER_DB_USER"
  -d "$MARKET_MASTER_DB_NAME"
  -At
)
coverage_json="[]"
database_ok=false
if [ "$epoch_active" = true ]; then
  uniswap_asset_guid="$(jq -r '.uniswap.asset_guid' <<<"$epoch_canaries")"
  uniswap_route_key="$(jq -r '.uniswap.route_key' <<<"$epoch_canaries")"
  uniswap_quote_notional="$(jq -r '.uniswap.quote_notional_usd' <<<"$epoch_canaries")"
  uniswap_selected_at="$(jq -r '.uniswap.selected_at' <<<"$epoch_canaries")"
  pancake_asset_guid="$(jq -r '.pancakeswap.asset_guid' <<<"$epoch_canaries")"
  pancake_route_key="$(jq -r '.pancakeswap.route_key' <<<"$epoch_canaries")"
  pancake_quote_notional="$(jq -r '.pancakeswap.quote_notional_usd' <<<"$epoch_canaries")"
  pancake_selected_at="$(jq -r '.pancakeswap.selected_at' <<<"$epoch_canaries")"
  if PGPASSWORD="$MARKET_MASTER_DB_PASSWORD" coverage_json="$(
    "${psql_command[@]}" \
      -v uniswap_asset_guid="$uniswap_asset_guid" \
      -v uniswap_route_key="$uniswap_route_key" \
      -v uniswap_quote_notional="$uniswap_quote_notional" \
      -v uniswap_selected_at="$uniswap_selected_at" \
      -v pancake_asset_guid="$pancake_asset_guid" \
      -v pancake_route_key="$pancake_route_key" \
      -v pancake_quote_notional="$pancake_quote_notional" \
      -v pancake_selected_at="$pancake_selected_at" \
      <<'SQL' 2>"$temp_dir/postgres-errors.log"
WITH requested_windows(hours) AS (
    VALUES (24), (48), (72)
),
canaries(
    provider,
    asset_guid,
    route_key,
    quote_notional_usd,
    selected_at
) AS (
    VALUES
      (
        'uniswap',
        :'uniswap_asset_guid'::text,
        :'uniswap_route_key'::text,
        :'uniswap_quote_notional'::numeric,
        :'uniswap_selected_at'::timestamptz
      ),
      (
        'pancakeswap',
        :'pancake_asset_guid'::text,
        :'pancake_route_key'::text,
        :'pancake_quote_notional'::numeric,
        :'pancake_selected_at'::timestamptz
      )
),
observations AS (
    SELECT requested_windows.hours,
           canaries.provider,
           canaries.asset_guid,
           canaries.route_key,
           canaries.quote_notional_usd,
           canaries.selected_at,
           quote.observed_at,
           LAG(quote.observed_at) OVER (
               PARTITION BY requested_windows.hours, canaries.provider
               ORDER BY quote.observed_at
           ) AS previous_at
    FROM requested_windows
    CROSS JOIN canaries
    JOIN dex_quote_observation quote
      ON quote.provider = canaries.provider
     AND quote.asset_guid = canaries.asset_guid
     AND quote.route_key = canaries.route_key
     AND quote.quote_notional_usd = canaries.quote_notional_usd
     AND quote.observed_at >= GREATEST(
           canaries.selected_at,
           now() - make_interval(hours => requested_windows.hours)
         )
),
summary AS (
    SELECT requested_windows.hours,
           canaries.provider,
           canaries.asset_guid,
           canaries.route_key,
           canaries.quote_notional_usd,
           canaries.selected_at,
           MIN(observations.observed_at) AS first_at,
           MAX(observations.observed_at) AS last_at,
           COUNT(observations.observed_at) AS samples,
           COALESCE(
               MAX(EXTRACT(EPOCH FROM (
                   observations.observed_at - observations.previous_at
               ))),
               0
           ) AS max_gap_seconds
    FROM requested_windows
    CROSS JOIN canaries
    LEFT JOIN observations
      ON observations.hours = requested_windows.hours
     AND observations.provider = canaries.provider
    GROUP BY requested_windows.hours,
             canaries.provider,
             canaries.asset_guid,
             canaries.route_key,
             canaries.quote_notional_usd,
             canaries.selected_at
),
evaluated AS (
    SELECT summary.*,
           selected_at <=
             now() - make_interval(hours => hours) AS window_elapsed,
           first_at <= GREATEST(
             selected_at,
             now() - make_interval(hours => hours)
           ) + interval '30 minutes' AS start_ok,
           last_at >= now() - interval '10 minutes' AS fresh_ok,
           max_gap_seconds <= 600 AS gap_ok
    FROM summary
)
SELECT jsonb_agg(
    jsonb_build_object(
        'hours', hours,
        'provider', provider,
        'mode', 'fixed_epoch_canary',
        'status', CASE
          WHEN NOT window_elapsed THEN 'observing'
          WHEN start_ok AND fresh_ok AND gap_ok THEN 'passed'
          ELSE 'failed'
        END,
        'window_elapsed', window_elapsed,
        'canary', jsonb_build_object(
          'asset_guid', asset_guid,
          'route_key', route_key,
          'quote_notional_usd', quote_notional_usd::text,
          'selected_at', to_char(
            selected_at AT TIME ZONE 'UTC',
            'YYYY-MM-DD"T"HH24:MI:SS"Z"'
          )
        ),
        'samples', samples,
        'max_gap_seconds', ROUND(max_gap_seconds::numeric, 2),
        'first_observed_at', first_at,
        'freshest_observation_at', last_at,
        'start_ok', COALESCE(start_ok, false),
        'fresh_ok', COALESCE(fresh_ok, false),
        'gap_ok', COALESCE(gap_ok, false)
    )
    ORDER BY hours, provider
)::text
FROM evaluated;
SQL
  )"; then
    database_ok=true
  else
    coverage_json="[]"
  fi
else
  if PGPASSWORD="$MARKET_MASTER_DB_PASSWORD" coverage_json="$("${psql_command[@]}" <<'SQL' 2>"$temp_dir/postgres-errors.log"
WITH requested_windows(hours) AS (
    VALUES (24), (48), (72)
),
providers(provider) AS (
    VALUES ('uniswap'), ('pancakeswap')
),
observations AS (
    SELECT requested_windows.hours,
           quote.provider,
           quote.asset_guid,
           quote.route_key,
           quote.quote_notional_usd,
           quote.observed_at,
           LAG(quote.observed_at) OVER (
               PARTITION BY requested_windows.hours,
                            quote.provider,
                            quote.asset_guid,
                            quote.route_key,
                            quote.quote_notional_usd
               ORDER BY quote.observed_at
           ) AS previous_at
    FROM requested_windows
    JOIN dex_quote_observation quote
      ON quote.observed_at >=
         now() - make_interval(hours => requested_windows.hours)
),
route_groups AS (
    SELECT hours,
           provider,
           asset_guid,
           route_key,
           quote_notional_usd,
           MIN(observed_at) AS first_at,
           MAX(observed_at) AS last_at,
           COUNT(*) AS samples,
           COALESCE(
               MAX(EXTRACT(EPOCH FROM (observed_at - previous_at))),
               0
           ) AS max_gap_seconds
    FROM observations
    GROUP BY hours, provider, asset_guid, route_key, quote_notional_usd
),
summary AS (
    SELECT requested_windows.hours,
           providers.provider,
           COUNT(route_groups.*) AS observed_groups,
           COUNT(route_groups.*) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
           ) AS full_window_groups,
           COUNT(route_groups.*) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
                 AND route_groups.last_at >= now() - interval '10 minutes'
                 AND route_groups.max_gap_seconds <= 600
           ) AS qualifying_groups,
           MAX(route_groups.samples) AS maximum_samples,
           MIN(route_groups.max_gap_seconds) FILTER (
               WHERE route_groups.first_at <=
                     now() - make_interval(hours => requested_windows.hours)
                     + interval '30 minutes'
           ) AS best_full_window_gap_seconds,
           MIN(route_groups.first_at) AS oldest_observation_at,
           MAX(route_groups.last_at) AS freshest_observation_at
    FROM requested_windows
    CROSS JOIN providers
    LEFT JOIN route_groups
      ON route_groups.hours = requested_windows.hours
     AND route_groups.provider = providers.provider
    GROUP BY requested_windows.hours, providers.provider
)
SELECT COALESCE(
    jsonb_agg(
        jsonb_build_object(
            'hours', hours,
            'provider', provider,
            'mode', 'diagnostic_dynamic',
            'status', CASE WHEN qualifying_groups > 0 THEN 'passed' ELSE 'pending' END,
            'observed_groups', observed_groups,
            'full_window_groups', full_window_groups,
            'qualifying_groups', qualifying_groups,
            'maximum_samples', COALESCE(maximum_samples, 0),
            'best_full_window_gap_seconds',
                ROUND(best_full_window_gap_seconds::numeric, 2),
            'oldest_observation_at', oldest_observation_at,
            'freshest_observation_at', freshest_observation_at
        )
        ORDER BY hours, provider
    ),
    '[]'::jsonb
)::text
FROM summary;
SQL
)"; then
    database_ok=true
  else
    coverage_json="[]"
  fi
fi

sample_ok=false
if [ "$site_http" = 200 ] &&
  [ "$funnel_health_http" = 200 ] &&
  [ "$unsigned_http" = 401 ] &&
  [ "$trading_http" = 200 ] &&
  [ "$system_http" = 200 ] &&
  [ "$uniswap_http" = 200 ] &&
  [ "$pancake_http" = 200 ] &&
  [ "$trading_state" = ready ] &&
  [ -z "$trading_last_error" ] &&
  { [ -z "$trading_outbox_state" ] || [ "$trading_outbox_state" = ready ]; } &&
  [ -z "$trading_outbox_last_error" ] &&
  [ "$tailscale_health_ok" = true ] &&
  [ "$disk_free_bytes" -ge $((25 * 1024 * 1024 * 1024)) ] &&
  [ -z "$retention_last_error" ] &&
  [ "$database_ok" = true ]; then
  sample_ok=true
fi

historical_complete=false
if [ "$epoch_active" = true ] && jq -e '
  length == 6 and
  all(.status == "passed")
' <<<"$coverage_json" >/dev/null 2>&1; then
  historical_complete=true
fi
historical_failed=false
if [ "$epoch_active" = true ] && jq -e '
  any(.status == "failed")
' <<<"$coverage_json" >/dev/null 2>&1; then
  historical_failed=true
fi

finished_epoch="$(date -u '+%s')"
finished_at="$(jq -nr --argjson epoch "$finished_epoch" '$epoch | todateiso8601')"
duration_ms=$(((finished_epoch - started_epoch) * 1000))
schedule_lag_ms=$(((started_epoch - scheduled_epoch) * 1000))
observed_at="$finished_at"
site_latency_ms="$(cat "$temp_dir/site.html.latency-ms")"
funnel_latency_ms="$(cat "$temp_dir/funnel-health.txt.latency-ms")"
trading_latency_ms="$(cat "$temp_dir/trading.json.latency-ms")"
system_latency_ms="$(cat "$temp_dir/system.json.latency-ms")"
uniswap_latency_ms="$(cat "$temp_dir/uniswap.json.latency-ms")"
pancake_latency_ms="$(cat "$temp_dir/pancakeswap.json.latency-ms")"
site_transport_error="$(error_summary "$temp_dir/site.html.error")"
funnel_transport_error="$(error_summary "$temp_dir/funnel-health.txt.error")"
unsigned_transport_error="$(error_summary "$temp_dir/unsigned.json.error")"
trading_transport_error="$(error_summary "$temp_dir/trading.json.error")"
system_transport_error="$(error_summary "$temp_dir/system.json.error")"
uniswap_transport_error="$(error_summary "$temp_dir/uniswap.json.error")"
pancake_transport_error="$(error_summary "$temp_dir/pancakeswap.json.error")"
failure_scope="none"
if [ "$sample_ok" != true ]; then
  if [ "$tailscale_health_ok" != true ]; then
    failure_scope="tailscale_health"
  elif [ "$site_http" = 000 ] &&
    [ "$funnel_health_http" = 000 ] &&
    [ "$trading_http" = 000 ] &&
    [ "$system_http" = 000 ] &&
    [ "$uniswap_http" = 000 ] &&
    [ "$pancake_http" = 000 ]; then
    failure_scope="observer_network"
  elif [ "$site_http" = 200 ] && {
    [ "$funnel_health_http" != 200 ] ||
      [ "$trading_http" = 502 ] || [ "$trading_http" = 504 ] ||
      [ "$system_http" = 502 ] || [ "$system_http" = 504 ] ||
      [ "$uniswap_http" = 502 ] || [ "$uniswap_http" = 504 ] ||
      [ "$pancake_http" = 502 ] || [ "$pancake_http" = 504 ];
  }; then
    failure_scope="backend_transport"
  else
    failure_scope="service_or_dependency"
  fi
fi
latest_report="$observation_dir/latest.json"
history_file="$observation_dir/production-soak.jsonl"
jq -n \
  --arg started_at "$started_at" \
  --arg scheduled_at "$scheduled_at" \
  --arg finished_at "$finished_at" \
  --arg observed_at "$observed_at" \
  --arg acceptance_epoch_id "$acceptance_epoch_id" \
  --arg expected_deployment_id "$expected_deployment_id" \
  --arg expected_deployment_url "$expected_deployment_url" \
  --arg expected_deployment_commit "$expected_deployment_commit" \
  --arg epoch_started_at "$epoch_started_at" \
  --argjson epoch_canaries "$epoch_canaries" \
  --arg epoch_active "$epoch_active" \
  --arg release_provenance_match "$release_provenance_match" \
  --arg trading_provenance "$trading_provenance" \
  --arg trading_release_commit "$trading_release_commit" \
  --arg trading_deployment_id "$trading_deployment_id" \
  --arg trading_deployment_url "$trading_deployment_url" \
  --arg system_provenance "$system_provenance" \
  --arg system_release_commit "$system_release_commit" \
  --arg system_deployment_id "$system_deployment_id" \
  --arg system_deployment_url "$system_deployment_url" \
  --arg uniswap_provenance "$uniswap_provenance" \
  --arg uniswap_release_commit "$uniswap_release_commit" \
  --arg uniswap_deployment_id "$uniswap_deployment_id" \
  --arg uniswap_deployment_url "$uniswap_deployment_url" \
  --arg pancake_provenance "$pancake_provenance" \
  --arg pancake_release_commit "$pancake_release_commit" \
  --arg pancake_deployment_id "$pancake_deployment_id" \
  --arg pancake_deployment_url "$pancake_deployment_url" \
  --arg duration_ms "$duration_ms" \
  --arg schedule_lag_ms "$schedule_lag_ms" \
  --arg production_origin "$production_origin" \
  --arg funnel_origin "$funnel_origin" \
  --arg site_http "$site_http" \
  --arg funnel_health_http "$funnel_health_http" \
  --arg unsigned_http "$unsigned_http" \
  --arg trading_http "$trading_http" \
  --arg system_http "$system_http" \
  --arg trading_state "$trading_state" \
  --arg trading_sequence "$trading_sequence" \
  --arg trading_last_error "$trading_last_error" \
  --arg trading_outbox_state "$trading_outbox_state" \
  --arg trading_outbox_last_error "$trading_outbox_last_error" \
  --arg sample_ok "$sample_ok" \
  --arg database_ok "$database_ok" \
  --arg historical_complete "$historical_complete" \
  --arg historical_failed "$historical_failed" \
  --arg disk_free_bytes "$disk_free_bytes" \
  --arg disk_state "$disk_state" \
  --arg retention_last_success_at "$retention_last_success_at" \
  --arg retention_last_error "$retention_last_error" \
  --arg site_latency_ms "$site_latency_ms" \
  --arg funnel_latency_ms "$funnel_latency_ms" \
  --arg trading_latency_ms "$trading_latency_ms" \
  --arg system_latency_ms "$system_latency_ms" \
  --arg uniswap_latency_ms "$uniswap_latency_ms" \
  --arg pancake_latency_ms "$pancake_latency_ms" \
  --arg failure_scope "$failure_scope" \
  --arg site_transport_error "$site_transport_error" \
  --arg funnel_transport_error "$funnel_transport_error" \
  --arg unsigned_transport_error "$unsigned_transport_error" \
  --arg trading_transport_error "$trading_transport_error" \
  --arg system_transport_error "$system_transport_error" \
  --arg uniswap_transport_error "$uniswap_transport_error" \
  --arg pancake_transport_error "$pancake_transport_error" \
  --arg tailscale_backend_state "$tailscale_backend_state" \
  --arg tailscale_health_ok "$tailscale_health_ok" \
  --argjson tailscale_health "$tailscale_health_json" \
  --arg runtime_release_commit "$runtime_release_commit" \
  --arg guardian_last_automatic_restart_at "$guardian_last_automatic_restart_at" \
  --arg network_interface "$network_interface" \
  --arg network_gateway "$network_gateway" \
  --arg network_ipv4 "$network_ipv4" \
  --argjson uniswap "$uniswap_summary" \
  --argjson pancakeswap "$pancake_summary" \
  --argjson coverage "$coverage_json" \
  '{
    schema_version: 4,
    acceptance_epoch_id: (
      if $acceptance_epoch_id == "" then null else $acceptance_epoch_id end
    ),
    acceptance_eligible: (
      $epoch_active == "true" and $release_provenance_match == "true"
    ),
    deployment_id: (
      if $trading_deployment_id == "" then null else $trading_deployment_id end
    ),
    deployment_url: (
      if $trading_deployment_url == "" then null else $trading_deployment_url end
    ),
    deployment_commit: (
      if $trading_release_commit == "" then null else $trading_release_commit end
    ),
    runtime_release_commit: $runtime_release_commit,
    scheduled_at: $scheduled_at,
    started_at: $started_at,
    finished_at: $finished_at,
    duration_ms: ($duration_ms | tonumber),
    schedule_lag_ms: ($schedule_lag_ms | tonumber),
    observed_at: $observed_at,
    status: (
      if $sample_ok != "true" then "failed"
      elif $historical_failed == "true" then "failed"
      elif $historical_complete == "true" then "passed"
      else "observing"
      end
    ),
    current_checks_status: (
      if $sample_ok == "true" then "passed" else "failed" end
    ),
    failure_scope: $failure_scope,
    transport_errors: {
      production_page: (if $site_transport_error == "" then null else $site_transport_error end),
      funnel_health: (if $funnel_transport_error == "" then null else $funnel_transport_error end),
      unsigned_funnel_rest: (if $unsigned_transport_error == "" then null else $unsigned_transport_error end),
      trading_bff: (if $trading_transport_error == "" then null else $trading_transport_error end),
      system_bff: (if $system_transport_error == "" then null else $system_transport_error end),
      uniswap_bff: (if $uniswap_transport_error == "" then null else $uniswap_transport_error end),
      pancakeswap_bff: (if $pancake_transport_error == "" then null else $pancake_transport_error end)
    },
    historical_acceptance_status: (
      if $historical_failed == "true" then "failed"
      elif $historical_complete == "true" then "passed"
      else "pending"
      end
    ),
    production_origin: $production_origin,
    funnel_origin: $funnel_origin,
    acceptance_epoch: {
      active: ($epoch_active == "true"),
      started_at: (
        if $epoch_started_at == "" then null else $epoch_started_at end
      ),
      expected_deployment_id: (
        if $expected_deployment_id == "" then null else $expected_deployment_id end
      ),
      expected_deployment_url: (
        if $expected_deployment_url == "" then null else $expected_deployment_url end
      ),
      expected_deployment_commit: (
        if $expected_deployment_commit == "" then null else $expected_deployment_commit end
      ),
      dex_canaries: (
        if $epoch_active == "true" then $epoch_canaries else null end
      )
    },
    release_provenance: {
      all_bff_probes_match: ($release_provenance_match == "true"),
      trading: {
        status: $trading_provenance,
        commit: $trading_release_commit,
        deployment_id: $trading_deployment_id,
        deployment_url: $trading_deployment_url
      },
      system: {
        status: $system_provenance,
        commit: $system_release_commit,
        deployment_id: $system_deployment_id,
        deployment_url: $system_deployment_url
      },
      uniswap: {
        status: $uniswap_provenance,
        commit: $uniswap_release_commit,
        deployment_id: $uniswap_deployment_id,
        deployment_url: $uniswap_deployment_url
      },
      pancakeswap: {
        status: $pancake_provenance,
        commit: $pancake_release_commit,
        deployment_id: $pancake_deployment_id,
        deployment_url: $pancake_deployment_url
      }
    },
    checks: {
      production_page_http: ($site_http | tonumber? // 0),
      funnel_health_http: ($funnel_health_http | tonumber? // 0),
      unsigned_funnel_rest_http: ($unsigned_http | tonumber? // 0),
      trading_bff_http: ($trading_http | tonumber? // 0),
      system_bff_http: ($system_http | tonumber? // 0),
      uniswap_bff_http: ($uniswap.http_status // 0),
      pancakeswap_bff_http: ($pancakeswap.http_status // 0),
      trading_state: $trading_state,
      trading_sequence: $trading_sequence,
      trading_last_error: $trading_last_error,
      trading_outbox_state: $trading_outbox_state,
      trading_outbox_last_error: $trading_outbox_last_error,
      database_ok: ($database_ok == "true"),
      disk_free_bytes: ($disk_free_bytes | tonumber? // 0),
      disk_state: $disk_state,
      retention_last_success_at: ($retention_last_success_at | tonumber? // 0),
      retention_last_error: $retention_last_error,
      tailscale_backend_state: $tailscale_backend_state,
      tailscale_health: $tailscale_health,
      tailscale_health_ok: ($tailscale_health_ok == "true"),
      guardian_last_automatic_restart_at: (
        $guardian_last_automatic_restart_at | tonumber? // 0
      ),
      network_interface: $network_interface,
      network_gateway: $network_gateway,
      network_ipv4: $network_ipv4
    },
    latency_ms: {
      production_page: ($site_latency_ms | tonumber? // 20000),
      funnel_health: ($funnel_latency_ms | tonumber? // 20000),
      trading_bff: ($trading_latency_ms | tonumber? // 20000),
      system_bff: ($system_latency_ms | tonumber? // 20000),
      uniswap_bff: ($uniswap_latency_ms | tonumber? // 20000),
      pancakeswap_bff: ($pancake_latency_ms | tonumber? // 20000)
    },
    dex: [$uniswap, $pancakeswap],
    historical_windows: $coverage
  }' > "$temp_dir/latest.json"

mv "$temp_dir/latest.json" "$latest_report"
jq -c . "$latest_report" >> "$history_file"
chmod 600 "$latest_report" "$history_file"
jq . "$latest_report"

if [ "$sample_ok" != true ]; then
  echo "Qiu Market production observation failed one or more current checks." >&2
  exit 1
fi
