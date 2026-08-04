#!/usr/bin/env bash
set -euo pipefail

action="${1:-status}"
if [ "$#" -gt 0 ]; then
  shift
fi

support_dir="$HOME/Library/Application Support/Qiu Market"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
observation_dir="${QIU_MARKET_OBSERVATION_DIR:-$support_dir/observations}"
epoch_file="${QIU_MARKET_ACCEPTANCE_EPOCH_FILE:-$observation_dir/acceptance-epoch.json}"
transport_smoke_file="${QIU_MARKET_TRANSPORT_SMOKE_FILE:-$observation_dir/transport-smoke.json}"
runtime_link="${QIU_MARKET_RUNTIME_LINK:-$support_dir/runtime-current}"
production_origin="${QIU_MARKET_PRODUCTION_ORIGIN:-https://qiu-market.vercel.app}"
database_env="${QIU_MARKET_DATABASE_ENV_FILE:-${QIU_MARKET_ENV_FILE:-$support_dir/production.env}}"

# shellcheck disable=SC1091
source "$repo_root/ops/macos/proxy-env.sh"
qiu_export_system_proxy

usage() {
  cat >&2 <<'USAGE'
Usage:
  manage-acceptance-epoch.sh status
  manage-acceptance-epoch.sh start \
    --deployment-id dpl_... \
    --deployment-url https://immutable-deployment.vercel.app \
    --commit <40-hex-sha> \
    [--epoch-id <id>] \
    [--production-origin https://qiu-market.vercel.app]
  manage-acceptance-epoch.sh stop

Start is allowed only when the Production BFF reports the exact immutable
deployment URL and release commit, and PostgreSQL has one stable six-hour
Uniswap and PancakeSwap canary. The acceptance window begins at the next UTC
wall-clock minute.
USAGE
  exit 2
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required acceptance epoch dependency is unavailable: $1" >&2
    exit 1
  fi
}

iso_at() {
  jq -nr --argjson epoch "$1" '$epoch | todateiso8601'
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

write_epoch() {
  local destination="$1"
  local payload="$2"
  local temp_file
  temp_file="$(mktemp "$observation_dir/.acceptance-epoch.XXXXXX")"
  printf '%s\n' "$payload" > "$temp_file"
  chmod 600 "$temp_file"
  mv "$temp_file" "$destination"
}

case "$action" in
  status)
    if [ ! -s "$epoch_file" ]; then
      echo "acceptance epoch: not-started"
      exit 0
    fi
    jq '{
      schema_version,
      epoch_id,
      status,
      production_origin,
      deployment_id,
      deployment_url,
      deployment_commit,
      dex_canaries,
      created_at,
      started_at,
      stopped_at
    }' "$epoch_file"
    ;;
  start)
    require_command curl
    require_command jq
    require_command psql
    deployment_id=""
    deployment_url=""
    deployment_commit=""
    epoch_id=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --deployment-id)
          [ "$#" -ge 2 ] || usage
          deployment_id="$2"
          shift 2
          ;;
        --deployment-url)
          [ "$#" -ge 2 ] || usage
          deployment_url="${2%/}"
          shift 2
          ;;
        --commit)
          [ "$#" -ge 2 ] || usage
          deployment_commit="$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')"
          shift 2
          ;;
        --epoch-id)
          [ "$#" -ge 2 ] || usage
          epoch_id="$2"
          shift 2
          ;;
        --production-origin)
          [ "$#" -ge 2 ] || usage
          production_origin="${2%/}"
          shift 2
          ;;
        *)
          usage
          ;;
      esac
    done
    if [[ ! "$deployment_id" =~ ^dpl_[A-Za-z0-9]+$ ]]; then
      echo "A valid immutable Vercel deployment ID is required." >&2
      exit 1
    fi
    if [[ ! "$deployment_url" =~ ^https://[A-Za-z0-9.-]+\.vercel\.app$ ]]; then
      echo "A valid immutable HTTPS Vercel deployment URL is required." >&2
      exit 1
    fi
    if [[ ! "$deployment_commit" =~ ^[0-9a-f]{40}$ ]]; then
      echo "A full 40-character release commit is required." >&2
      exit 1
    fi
    if [[ ! "$production_origin" =~ ^https://[A-Za-z0-9.-]+$ ]]; then
      echo "A valid HTTPS Production origin is required." >&2
      exit 1
    fi
    if [ "$deployment_url" = "$production_origin" ]; then
      echo "The Production alias is not an immutable deployment URL." >&2
      exit 1
    fi
    runtime_manifest="$runtime_link/runtime-manifest.env"
    runtime_commit=""
    if [ -L "$runtime_link" ] && [ -f "$runtime_manifest" ]; then
      runtime_commit="$(sed -n 's/^git_commit=//p' "$runtime_manifest" | head -1)"
    fi
    acceptance_now="${QIU_MARKET_ACCEPTANCE_NOW_EPOCH:-$(date -u '+%s')}"
    if [[ ! "$runtime_commit" =~ ^[0-9a-f]{40}$ ]] ||
      [[ ! "$acceptance_now" =~ ^[0-9]+$ ]] ||
      ! jq -e \
        --arg deployment_id "$deployment_id" \
        --arg deployment_url "$deployment_url" \
        --arg deployment_commit "$deployment_commit" \
        --arg runtime_commit "$runtime_commit" \
        --argjson now "$acceptance_now" '
          .schema_version == 1 and
          .status == "passed" and
          .deployment_id == $deployment_id and
          .deployment_url == $deployment_url and
          .deployment_commit == $deployment_commit and
          .runtime_release_commit == $runtime_commit and
          (.completed_at | fromdateiso8601) <= $now and
          ($now - (.completed_at | fromdateiso8601)) <= 1800 and
          .result.status == "passed" and
          ([.result.acceptance[]] | all)
        ' "$transport_smoke_file" >/dev/null 2>&1; then
      echo "A passing 30-minute transport smoke for this exact release is required within the previous 30 minutes." >&2
      exit 1
    fi
    if [ -s "$epoch_file" ] &&
      [ "$(jq -r '.status // ""' "$epoch_file")" = active ]; then
      echo "An acceptance epoch is already active; stop it explicitly first." >&2
      exit 1
    fi
    if [ ! -f "$database_env" ] || [ -L "$database_env" ]; then
      echo "Qiu Market private production environment is unavailable." >&2
      exit 1
    fi
    database_env_mode="$(stat -f '%Lp' "$database_env")"
    if [ "$database_env_mode" != 600 ] && [ "$database_env_mode" != 400 ]; then
      echo "Qiu Market private production environment must have mode 0600 or 0400." >&2
      exit 1
    fi
    # The repository environment carries non-secret database coordinates while
    # the private production environment carries the password and overrides.
    # shellcheck disable=SC1091
    source "$repo_root/ops/macos/production-lib.sh"
    QIU_MARKET_ENV_FILE="$database_env"
    qiu_load_production_environment "$repo_root"
    qiu_require_private_environment
    if [ -z "$QIU_MARKET_DB_HOST" ] ||
      [ -z "$QIU_MARKET_DB_PORT" ] ||
      [ -z "$QIU_MARKET_DB_USER" ] ||
      [ -z "$QIU_MARKET_DB_NAME" ]; then
      echo "Merged Qiu Market database configuration is incomplete." >&2
      exit 1
    fi
    case "$QIU_MARKET_DB_HOST" in
      127.0.0.1|localhost|::1) ;;
      *)
        echo "Acceptance epoch database access must remain loopback-only." >&2
        exit 1
        ;;
    esac

    mkdir -p "$observation_dir"
    chmod 700 "$observation_dir"
    headers_file="$(mktemp "$observation_dir/.epoch-probe-headers.XXXXXX")"
    body_file="$(mktemp "$observation_dir/.epoch-probe-body.XXXXXX")"
    cleanup() {
      rm -f -- "$headers_file" "$body_file"
    }
    trap cleanup EXIT
    probe_http="$(
      curl --silent --show-error --max-time 20 \
        --dump-header "$headers_file" \
        --output "$body_file" \
        --write-out '%{http_code}' \
        "$production_origin/api/v1/trading/markets/BTC-USDT/status" || true
    )"
    observed_status="$(header_value "$headers_file" X-Qiu-Market-Provenance)"
    observed_commit="$(header_value "$headers_file" X-Qiu-Market-Release-Commit)"
    observed_id="$(header_value "$headers_file" X-Qiu-Market-Deployment-ID)"
    observed_url="$(header_value "$headers_file" X-Qiu-Market-Deployment-URL)"
    if [ "$probe_http" != 200 ] ||
      [ "$observed_status" != VERIFIED ] ||
      [ "$observed_commit" != "$deployment_commit" ] ||
      [ "$observed_id" != "$deployment_id" ] ||
      [ "${observed_url%/}" != "$deployment_url" ]; then
      echo "Production BFF does not report the requested immutable release identity." >&2
      jq -n \
        --arg http "$probe_http" \
        --arg provenance "$observed_status" \
        --arg expected_id "$deployment_id" \
        --arg observed_id "$observed_id" \
        --arg expected_commit "$deployment_commit" \
        --arg observed_commit "$observed_commit" \
        --arg expected_url "$deployment_url" \
        --arg observed_url "$observed_url" \
        '{
          http_status: ($http | tonumber? // 0),
          provenance: $provenance,
          expected_deployment_id: $expected_id,
          observed_deployment_id: $observed_id,
          expected_commit: $expected_commit,
          observed_commit: $observed_commit,
          expected_deployment_url: $expected_url,
          observed_deployment_url: $observed_url
        }' >&2
      exit 1
    fi

    if ! dex_canaries="$(
      qiu_psql <<'SQL'
WITH observations AS (
    SELECT quote.provider,
           quote.asset_guid,
           quote.route_key,
           quote.quote_notional_usd,
           quote.observed_at,
           LAG(quote.observed_at) OVER (
               PARTITION BY quote.provider,
                            quote.asset_guid,
                            quote.route_key,
                            quote.quote_notional_usd
               ORDER BY quote.observed_at
           ) AS previous_at
    FROM dex_quote_observation quote
    WHERE quote.observed_at >= now() - interval '6 hours'
      AND quote.provider IN ('uniswap', 'pancakeswap')
),
route_groups AS (
    SELECT provider,
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
    GROUP BY provider, asset_guid, route_key, quote_notional_usd
),
ranked AS (
    SELECT route_groups.*,
           ROW_NUMBER() OVER (
               PARTITION BY provider
               ORDER BY max_gap_seconds ASC,
                        samples DESC,
                        quote_notional_usd DESC,
                        asset_guid,
                        route_key
           ) AS candidate_rank
    FROM route_groups
    WHERE first_at <= now() - interval '5 hours 50 minutes'
      AND last_at >= now() - interval '10 minutes'
      AND max_gap_seconds <= 600
)
SELECT COALESCE(
    jsonb_object_agg(
        provider,
        jsonb_build_object(
            'asset_guid', asset_guid,
            'route_key', route_key,
            'quote_notional_usd', quote_notional_usd::text,
            'selection_window_hours', 6,
            'selected_observation_count', samples,
            'selected_max_gap_seconds',
                ROUND(max_gap_seconds::numeric, 2),
            'first_observed_at', first_at,
            'last_observed_at', last_at
        )
    ),
    '{}'::jsonb
)::text
FROM ranked
WHERE candidate_rank = 1;
SQL
    )"; then
      echo "Could not select fixed DEX acceptance canaries." >&2
      exit 1
    fi
    if ! jq -e '
      (keys | sort) == ["pancakeswap", "uniswap"] and
      all(.[];
        (.asset_guid | type == "string" and
          test("^[0-9a-fA-F-]{36}$")) and
        (.route_key | type == "string" and length > 0 and length <= 256) and
        (.quote_notional_usd | type == "string" and
          test("^[0-9]+([.][0-9]+)?$")) and
        (.selected_observation_count | type == "number" and . > 0) and
        (.selected_max_gap_seconds | type == "number" and . <= 600)
      )
    ' <<<"$dex_canaries" >/dev/null 2>&1; then
      echo "Both DEX providers need one stable six-hour canary before the epoch can start." >&2
      exit 1
    fi

    mkdir -p "$observation_dir/archive"
    chmod 700 "$observation_dir/archive"
    if [ -s "$epoch_file" ]; then
      archived_at="$(date -u '+%Y%m%dT%H%M%SZ')"
      archive_id="$(
        jq -r '.epoch_id // "unknown"' "$epoch_file" |
          tr -cd 'A-Za-z0-9._-'
      )"
      archive_target="$observation_dir/archive/acceptance-epoch-$archive_id-$archived_at.json"
      if [ -e "$archive_target" ]; then
        echo "Refusing to overwrite an existing acceptance epoch archive." >&2
        exit 1
      fi
      cp "$epoch_file" "$archive_target"
      chmod 600 "$archive_target"
    fi

    created_epoch="$(date -u '+%s')"
    started_epoch=$(((created_epoch / 60 + 1) * 60))
    created_at="$(iso_at "$created_epoch")"
    started_at="$(iso_at "$started_epoch")"
    if [ -z "$epoch_id" ]; then
      epoch_id="qiu-market-$(date -u '+%Y%m%dT%H%M%SZ')-$RANDOM"
    fi
    if [[ ! "$epoch_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$ ]]; then
      echo "The acceptance epoch ID must be 8-128 safe characters." >&2
      exit 1
    fi
    payload="$(
      jq -n \
        --arg epoch_id "$epoch_id" \
        --arg production_origin "$production_origin" \
        --arg deployment_id "$deployment_id" \
        --arg deployment_url "$deployment_url" \
        --arg deployment_commit "$deployment_commit" \
        --arg created_at "$created_at" \
        --arg started_at "$started_at" \
        --argjson dex_canaries "$dex_canaries" \
        '{
          schema_version: 2,
          epoch_id: $epoch_id,
          status: "active",
          production_origin: $production_origin,
          deployment_id: $deployment_id,
          deployment_url: $deployment_url,
          deployment_commit: $deployment_commit,
          dex_canaries: (
            $dex_canaries |
            with_entries(.value += {selected_at: $started_at})
          ),
          created_at: $created_at,
          started_at: $started_at,
          stopped_at: null
        }'
    )"
    write_epoch "$epoch_file" "$payload"
    echo "Acceptance epoch starts at $started_at on the verified release."
    jq . "$epoch_file"
    ;;
  stop)
    require_command jq
    if [ ! -s "$epoch_file" ]; then
      echo "No acceptance epoch exists." >&2
      exit 1
    fi
    if [ "$(jq -r '.status // ""' "$epoch_file")" != active ]; then
      echo "Acceptance epoch is already stopped."
      exit 0
    fi
    mkdir -p "$observation_dir"
    stopped_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    payload="$(
      jq --arg stopped_at "$stopped_at" '
        .status = "stopped" |
        .stopped_at = $stopped_at
      ' "$epoch_file"
    )"
    write_epoch "$epoch_file" "$payload"
    echo "Acceptance epoch stopped at $stopped_at; evidence was preserved."
    ;;
  *)
    usage
    ;;
esac
