#!/bin/bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d /tmp/qiu-market-t1-postgres.XXXXXX)"
pgdata="$fixture/pg"
runtime="$fixture/runtime"
port="$((25000 + RANDOM % 10000))"
http_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)"
grpc_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
PY
)"
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ "$status" -ne 0 ] && [ -d "$runtime/logs" ]; then
    for log in "$runtime"/logs/*.err.log; do
      [ -f "$log" ] && { echo "--- $(basename "$log")" >&2; tail -n 40 "$log" >&2; }
    done
  fi
  if [ -x "$repo_root/trading/scripts/practice-local.sh" ] && [ -d "$runtime" ]; then
    QIU_T1_HTTP_PORT="$http_port" QIU_T1_GRPC_PORT="$grpc_port" \
      "$repo_root/trading/scripts/practice-local.sh" stop "$runtime" >/dev/null 2>&1 || true
  fi
  if [ -f "$pgdata/postmaster.pid" ]; then
    pg_ctl -D "$pgdata" -m immediate stop >/dev/null 2>&1 || true
  fi
  find "$fixture" -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

initdb -D "$pgdata" -A trust -U "$(id -un)" >/dev/null
pg_ctl -D "$pgdata" -o "-h 127.0.0.1 -p $port" -w start >/dev/null
createdb -h 127.0.0.1 -p "$port" qiu_t1_state
createdb -h 127.0.0.1 -p "$port" qiu_t1_closure_state
createdb -h 127.0.0.1 -p "$port" qiu_t1_reference
for state_database in qiu_t1_state qiu_t1_closure_state; do
psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$port" "$state_database" <<'SQL' >/dev/null
CREATE TABLE qiu_trading_practice_owner (
  singleton boolean PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  owner_key text NOT NULL
);
INSERT INTO qiu_trading_practice_owner(singleton, owner_key)
VALUES (TRUE, 'qiu-market/trading-practice/v1');
SQL
done
psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$port" qiu_t1_reference <<'SQL' >/dev/null
CREATE TABLE qiu_t1_reference_probe(value integer);
SQL

for database in qiu_t1_state qiu_t1_closure_state qiu_t1_reference; do
  for migration in "$repo_root"/migrations/*.sql; do
    psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$port" "$database" -f "$migration" >/dev/null
  done
done

psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$port" qiu_t1_reference <<'SQL' >/dev/null
INSERT INTO asset(guid, asset_name, asset_symbol, asset_logo, is_active)
VALUES ('qiu-t1-btc', 'Bitcoin', 'BTC', 'https://example.invalid/btc.svg', TRUE)
ON CONFLICT (guid) DO NOTHING;
INSERT INTO asset_external_mapping(provider, asset_guid, external_id)
VALUES ('coingecko', 'qiu-t1-btc', 'bitcoin')
ON CONFLICT DO NOTHING;
INSERT INTO asset_price_index(
  asset_guid, price_usd, open_24h_usd, change_24h_pct, turnover_24h_usd,
  contributor_count, confidence, available, observed_at, contributors, exclusions
) VALUES (
  'qiu-t1-btc', 60000, 59000, 1.694915254237288136, 1000000000,
  2, 'medium', TRUE, clock_timestamp(), '["fixture-cex-a","fixture-cex-b"]', '[]'
)
ON CONFLICT (asset_guid) DO UPDATE SET
  price_usd=EXCLUDED.price_usd,
  contributor_count=EXCLUDED.contributor_count,
  confidence=EXCLUDED.confidence,
  available=EXCLUDED.available,
  observed_at=clock_timestamp(),
  contributors=EXCLUDED.contributors;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='qiu_t1_reference_reader') THEN
    CREATE ROLE qiu_t1_reference_reader LOGIN;
  END IF;
END $$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE qiu_t1_reference TO qiu_t1_reference_reader;
GRANT USAGE ON SCHEMA public TO qiu_t1_reference_reader;
GRANT SELECT ON asset_price_index, asset_external_mapping TO qiu_t1_reference_reader;
SQL

export S78_TEST_POSTGRES_DSN="postgres://$(id -un)@127.0.0.1:$port/qiu_t1_state?sslmode=disable"
export QIU_T1_TEST_STATE_DSN="$S78_TEST_POSTGRES_DSN"
export QIU_T1_CLOSURE_STATE_DSN="postgres://$(id -un)@127.0.0.1:$port/qiu_t1_closure_state?sslmode=disable"
export QIU_T1_TEST_REFERENCE_DSN="postgres://qiu_t1_reference_reader@127.0.0.1:$port/qiu_t1_reference?sslmode=disable"
cd "$repo_root"
go test ./trading/gateway -run '^TestPracticeStateOwnershipMarker$' -count=1
go test ./trading/readmodel/postgres \
  -run '^TestReaderAccountScopeKeysetsTimelineLedgerAndRebuild$' -count=1

go test ./trading/service -run '^(TestPracticePostgresBoundaryUsesServerIdentityOwnerAndReadOnlySession|TestPracticeDeterministicMakerPartialCancelReplayAndRestart)$' -count=1

install -d -m 700 "$runtime/config" "$runtime/secrets" "$runtime/migrations"
cp "$repo_root"/migrations/*.sql "$runtime/migrations/"
chmod 600 "$runtime/migrations/"*.sql
printf 'postgres://%s@127.0.0.1:%s/qiu_t1_state?sslmode=disable\n' "$(id -un)" "$port" > "$runtime/config/state-postgres-dsn"
printf 'postgres://qiu_t1_reference_reader@127.0.0.1:%s/qiu_t1_reference?sslmode=disable\n' "$port" > "$runtime/config/reference-postgres-dsn"
printf '%s\n' 'fixture:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' > "$runtime/secrets/cursor-hmac-current"
chmod 600 "$runtime/config/"* "$runtime/secrets/"*

go build -o "$fixture/market-services" ./cmd/market-services
chmod 700 "$fixture/market-services"
manager="$repo_root/trading/scripts/practice-local.sh"
export QIU_T1_HTTP_PORT="$http_port" QIU_T1_GRPC_PORT="$grpc_port"
export QIU_T1_FRONTEND_ORIGIN='http://127.0.0.1:15174'
"$manager" start "$runtime" "$fixture/market-services" >/dev/null
"$manager" status "$runtime" | grep -E "^practice stack running trading_pid=[1-9][0-9]* gateway_pid=[1-9][0-9]* http=127.0.0.1:$http_port grpc=127.0.0.1:$grpc_port$" >/dev/null

origin='http://127.0.0.1:15174'
base="http://127.0.0.1:$http_port"
cookie_jar="$fixture/cookies.txt"
capabilities="$fixture/capabilities.json"
login="$fixture/login.json"
funding="$fixture/funding.json"

curl --noproxy '*' -fsS --max-time 3 "$base/healthz" >/dev/null
curl --noproxy '*' -fsS --max-time 3 "$base/api/v1/trading/auth/capabilities" > "$capabilities"
python3 - "$capabilities" <<'PY'
import json, sys
value = json.load(open(sys.argv[1]))
assert value == {
  'github_oauth_enabled': False,
  'local_login_enabled': True,
  'recovery_gate_enabled': False,
  'practice_mode_enabled': True,
  'starter_funds_enabled': True,
  'virtual_liquidity_enabled': True,
}, value
PY
curl --noproxy '*' -fsS --max-time 3 -c "$cookie_jar" \
  -H "Origin: $origin" -H 'Content-Type: application/json' \
  -d '{}' "$base/api/v1/trading/auth/local" > "$login"
python3 - "$login" <<'PY'
import json, sys
value = json.load(open(sys.argv[1]))
assert value['local'] is True
assert value['principal']['account_id'] == 'github:qianqiu0404'
assert value['principal']['admin'] is True
PY
csrf="$(awk '$6 == "s78_trading_csrf" { print $7 }' "$cookie_jar")"
[ -n "$csrf" ]

for request in \
  '{"request_id":"starter-v1-usdt","asset":"USDT","amount":"10000"}' \
  '{"request_id":"starter-v1-btc","asset":"BTC","amount":"0.1"}'; do
  curl --noproxy '*' -fsS --max-time 5 -b "$cookie_jar" \
    -H "Origin: $origin" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
    -d "$request" "$base/api/v1/trading/admin/fund" >/dev/null
done
curl --noproxy '*' -fsS --max-time 5 -b "$cookie_jar" \
  "$base/api/v1/trading/account/funding/starter-v1-usdt" > "$funding"
python3 - "$funding" <<'PY'
import json, sys
value = json.load(open(sys.argv[1]))
assert value['market_id'] == 'BTC-USDT'
assert value['request_id'] == 'starter-v1-usdt'
assert value['funding_event_id']
assert int(value['sequence']) > 0
assert value['asset'] == 'USDT'
assert value['amount'] == '10000'
assert value['projection_result'] == 'applied'
assert value['ledger_balanced'] is True
assert value['occurred_at']
PY

event_count="$(psql -X -At -h 127.0.0.1 -p "$port" qiu_t1_state -c \
  "SELECT count(*) FROM trading_event_batch WHERE account_id='github:qianqiu0404' AND operation=1 AND request_id IN ('starter-v1-usdt','starter-v1-btc')")"
[ "$event_count" = 2 ]
reference_writes="$(psql -X -At -h 127.0.0.1 -p "$port" qiu_t1_reference -c \
  "SELECT count(*) FROM pg_stat_activity WHERE datname='qiu_t1_reference' AND application_name <> 'psql' AND state <> 'idle' AND query ~* '^(insert|update|delete|create|alter|drop)'")"
[ "$reference_writes" = 0 ]

"$manager" stop "$runtime" >/dev/null
for role in trading gateway; do
  [ ! -e "$runtime/run/$role.pid" ] && [ ! -e "$runtime/run/$role.owner" ]
done
if nc -z 127.0.0.1 "$http_port" 2>/dev/null || nc -z 127.0.0.1 "$grpc_port" 2>/dev/null; then
  echo 'practice ports remained open after bounded stop' >&2
  exit 1
fi
echo 'Practice PostgreSQL identity, ownership, and read-only fixtures passed.'
echo 'Browser-equivalent HTTP -> isolated trading gateway -> gRPC -> Practice PostgreSQL fixture passed.'
