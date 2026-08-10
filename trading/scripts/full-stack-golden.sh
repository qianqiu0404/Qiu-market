#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd -P)"
task_tmp_root="${TMPDIR:-/tmp}"
run_root="$(mktemp -d "${task_tmp_root%/}/qiu-full-stack-golden.XXXXXX")"
chmod 700 "$run_root"

postgres_pid=""
fixture_pid=""
vue_pid=""
coordinator_pid=""
backend_a_pid=""
backend_b_pid=""
postgres_port=""
fixture_port=""
frontend_port=""
api_port=""
grpc_port=""
pg_bin_dir=""

free_port() {
  /usr/bin/python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

wait_http() {
  local url="$1"
  local ca="${2:-}"
  local attempt
  for attempt in $(seq 1 200); do
    if [[ -n "$ca" ]]; then
      curl --noproxy '*' --silent --show-error --fail --cacert "$ca" "$url" >/dev/null 2>&1 && return 0
    else
      curl --noproxy '*' --silent --show-error --fail "$url" >/dev/null 2>&1 && return 0
    fi
    sleep 0.1
  done
  return 1
}

port_is_closed() {
  /usr/bin/python3 - "$1" <<'PY'
import socket, sys
s = socket.socket()
s.settimeout(0.15)
try:
    result = s.connect_ex(("127.0.0.1", int(sys.argv[1])))
finally:
    s.close()
raise SystemExit(0 if result != 0 else 1)
PY
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ $status -ne 0 ]]; then
    for log in coordinator fixture preview postgres; do
      if [[ -f "$run_root/$log.log" ]]; then
        echo "[full-stack-golden] tail $log.log" >&2
        tail -n 80 "$run_root/$log.log" >&2 || true
      fi
    done
  fi
  stop_pid() {
    local pid="$1" attempt
    [[ "$pid" =~ ^[0-9]+$ && "$pid" -gt 1 ]] || return 0
    if kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid" 2>/dev/null || true; fi
    for attempt in $(seq 1 100); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.05
    done
    if kill -0 "$pid" 2>/dev/null; then kill -KILL "$pid" 2>/dev/null || true; fi
    wait "$pid" 2>/dev/null || true
  }
  if [[ -n "$api_port" ]]; then
    curl --noproxy '*' --silent --max-time 0.5 "http://127.0.0.1:$api_port/__full-stack/evidence" >"$run_root/cleanup-evidence.json" 2>/dev/null || true
    if [[ -s "$run_root/cleanup-evidence.json" ]]; then
      read -r backend_a_pid backend_b_pid < <(/usr/bin/python3 - "$run_root/cleanup-evidence.json" <<'PY'
import json, sys
try:
    value = json.load(open(sys.argv[1], encoding="utf-8"))
    def live_pid(name):
        process = value.get(name, {})
        return 0 if process.get("exited", False) else int(process.get("pid", 0) or 0)
    print(live_pid("backend_a"), live_pid("backend_b"))
except Exception:
    print(0, 0)
PY
      )
    fi
  fi
  if [[ -n "$coordinator_pid" ]]; then
    active_child="$(/usr/bin/pgrep -P "$coordinator_pid" 2>/dev/null | sed -n '1p' || true)"
    if [[ -n "$active_child" && "$active_child" != "$backend_a_pid" && "$active_child" != "$backend_b_pid" ]]; then
      backend_b_pid="$active_child"
    fi
  fi
  stop_pid "$backend_b_pid"
  stop_pid "$backend_a_pid"
  stop_pid "$coordinator_pid"
  stop_pid "$vue_pid"
  stop_pid "$fixture_pid"
  if [[ -n "$pg_bin_dir" && -f "$run_root/pgdata/postmaster.pid" ]]; then
    "$pg_bin_dir/pg_ctl" -D "$run_root/pgdata" -m immediate -w stop >/dev/null 2>&1 || true
  fi
  local pids_stopped=true ports_closed=true temp_removed=true clean=true
  for pid in "$coordinator_pid" "$backend_a_pid" "$backend_b_pid" "$vue_pid" "$fixture_pid" "$postgres_pid"; do
    if [[ "$pid" =~ ^[0-9]+$ && "$pid" -gt 1 ]] && kill -0 "$pid" 2>/dev/null; then pids_stopped=false; fi
  done
  for port in "$postgres_port" "$fixture_port" "$frontend_port" "$api_port" "$grpc_port"; do
    if [[ -n "$port" ]] && ! port_is_closed "$port"; then ports_closed=false; fi
  done
  find "$run_root" -depth -delete 2>/dev/null || temp_removed=false
  if [[ -e "$run_root" ]]; then temp_removed=false; fi
  if [[ "$pids_stopped" != true || "$ports_closed" != true || "$temp_removed" != true ]]; then clean=false; fi
  echo "[full-stack-golden] cleanup_complete=$clean pids_stopped=$pids_stopped ports_closed=$ports_closed temp_removed=$temp_removed"
  if [[ "$clean" != true && $status -eq 0 ]]; then status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ -n "${QIU_GO_BIN:-}" ]]; then
  go_bin="$QIU_GO_BIN"
elif command -v go >/dev/null 2>&1; then
  go_bin="$(command -v go)"
elif [[ -x "$repo_root/../toolchains/go/bin/go" ]]; then
  go_bin="$repo_root/../toolchains/go/bin/go"
else
  echo "missing Go toolchain; set QIU_GO_BIN to an absolute executable" >&2
  exit 2
fi
[[ "$go_bin" = /* && -x "$go_bin" ]] || { echo "QIU_GO_BIN must be an absolute executable" >&2; exit 2; }
npm_bin="$(command -v npm || true)"
[[ "$npm_bin" = /* && -x "$npm_bin" ]] || { echo "missing absolute npm executable" >&2; exit 2; }
runtime_path="$(dirname "$go_bin"):$(dirname "$npm_bin"):/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

if [[ -n "${QIU_TEST_POSTGRES_BIN_DIR:-}" ]]; then
  pg_bin_dir="$QIU_TEST_POSTGRES_BIN_DIR"
elif command -v postgres >/dev/null 2>&1 && command -v initdb >/dev/null 2>&1 && command -v pg_ctl >/dev/null 2>&1; then
  pg_bin_dir="$(dirname "$(command -v postgres)")"
else
  cache_list="$run_root/postgres-cache-list"
  find "$repo_root/.." -type f -path '*/pg/bin/postgres' -perm -111 -print >"$cache_list"
  cache_count="$(wc -l <"$cache_list" | tr -d ' ')"
  if [[ "$cache_count" != 1 ]]; then
    echo "no unique verified PostgreSQL cache; set QIU_TEST_POSTGRES_BIN_DIR (found $cache_count candidates)" >&2
    exit 2
  fi
  pg_bin_dir="$(dirname "$(sed -n '1p' "$cache_list")")"
fi
[[ "$pg_bin_dir" = /* ]] || { echo "PostgreSQL bin directory must be absolute" >&2; exit 2; }
for program in postgres initdb pg_ctl; do
  [[ -x "$pg_bin_dir/$program" ]] || { echo "missing PostgreSQL executable: $pg_bin_dir/$program" >&2; exit 2; }
done
postgres_version="$($pg_bin_dir/postgres --version | sed 's/^postgres (PostgreSQL) //')"
[[ "$postgres_version" == "16.14" ]] || { echo "PostgreSQL 16.14 is required, found $postgres_version" >&2; exit 2; }

ports=()
while [[ ${#ports[@]} -lt 5 ]]; do
  candidate="$(free_port)"
  duplicate=false
  for port in "${ports[@]:-}"; do [[ "$port" == "$candidate" ]] && duplicate=true; done
  [[ "$duplicate" == false ]] && ports+=("$candidate")
done
postgres_port="${ports[0]}"; fixture_port="${ports[1]}"; frontend_port="${ports[2]}"; api_port="${ports[3]}"; grpc_port="${ports[4]}"

mkdir -p "$run_root/bin" "$run_root/socket" "$run_root/tmp" "$run_root/go-cache" "$run_root/npm-cache"
"$go_bin" build -race -o "$run_root/bin/full-stack-golden" ./cmd/full-stack-golden
/usr/bin/openssl req -x509 -newkey rsa:2048 -sha256 -nodes -keyout "$run_root/fixture-key.pem" -out "$run_root/fixture-cert.pem" -days 1 -subj '/CN=127.0.0.1' -addext 'subjectAltName=IP:127.0.0.1' >/dev/null 2>&1
chmod 600 "$run_root/fixture-key.pem" "$run_root/fixture-cert.pem"

"$pg_bin_dir/initdb" -D "$run_root/pgdata" --auth=trust --no-locale -U postgres >/dev/null
"$pg_bin_dir/pg_ctl" -D "$run_root/pgdata" -l "$run_root/postgres.log" -o "-F -p $postgres_port -h 127.0.0.1 -k $run_root/socket" -w start >/dev/null
postgres_pid="$(sed -n '1p' "$run_root/pgdata/postmaster.pid")"
postgres_dsn="postgres://postgres@127.0.0.1:$postgres_port/postgres?sslmode=disable"

env -i PATH="$runtime_path" TMPDIR="$run_root/tmp" LC_ALL=C "$run_root/bin/full-stack-golden" \
  --role fixture --fixture-address "127.0.0.1:$fixture_port" --fixture-cert "$run_root/fixture-cert.pem" --fixture-key "$run_root/fixture-key.pem" \
  >"$run_root/fixture.log" 2>&1 &
fixture_pid=$!
wait_http "https://127.0.0.1:$fixture_port/__fixture/evidence" "$run_root/fixture-cert.pem"

api_origin="http://127.0.0.1:$api_port"
frontend_origin="http://127.0.0.1:$frontend_port"
if [[ ! -x "$repo_root/frontend/node_modules/.bin/playwright" || ! -x "$repo_root/frontend/node_modules/.bin/vite" ]]; then
  env -i PATH="$runtime_path" HOME="$run_root" TMPDIR="$run_root/tmp" LC_ALL=C npm_config_cache="$run_root/npm-cache" npm --prefix "$repo_root/frontend" ci
fi
env -i PATH="$runtime_path" HOME="$run_root" TMPDIR="$run_root/tmp" LC_ALL=C npm_config_cache="$run_root/npm-cache" npm --prefix "$repo_root/frontend" run build
env -i PATH="$runtime_path" TMPDIR="$run_root/tmp" LC_ALL=C npm_config_cache="$run_root/npm-cache" VITE_API_PROXY_TARGET="$api_origin" \
  npm --prefix "$repo_root/frontend" run preview -- --host 127.0.0.1 --port "$frontend_port" --strictPort >"$run_root/preview.log" 2>&1 &
vue_pid=$!
wait_http "$frontend_origin/"

manifest="$run_root/manifest.json"
env -i PATH="$runtime_path" TMPDIR="$run_root/tmp" LC_ALL=C QIU_FULLSTACK_POSTGRES_DSN="$postgres_dsn" "$run_root/bin/full-stack-golden" \
  --role coordinator --repo-root "$repo_root" --http-address "127.0.0.1:$api_port" --grpc-address "127.0.0.1:$grpc_port" \
  --frontend-origin "$frontend_origin" --manifest "$manifest" --postgres-pid "$postgres_pid" --postgres-version "$postgres_version" \
  --fixture-pid "$fixture_pid" --fixture-origin "https://127.0.0.1:$fixture_port" --fixture-ca "$run_root/fixture-cert.pem" --vue-pid "$vue_pid" \
  >"$run_root/coordinator.log" 2>&1 &
coordinator_pid=$!
wait_http "$api_origin/__full-stack/ready"
[[ -f "$manifest" && "$(stat -f '%Lp' "$manifest")" == 600 ]] || { echo "manifest is missing or not 0600" >&2; exit 1; }

echo "[full-stack-golden] postgres=$postgres_version pg_pid=$postgres_pid fixture_pid=$fixture_pid coordinator_pid=$coordinator_pid vue_pid=$vue_pid"
echo "[full-stack-golden] api=$api_origin frontend=$frontend_origin manifest=$manifest"

env -i PATH="$runtime_path" HOME="$run_root" TMPDIR="$run_root/tmp" LC_ALL=C npm_config_cache="$run_root/npm-cache" \
  QIU_FULLSTACK_FRONTEND_ORIGIN="$frontend_origin" QIU_FULLSTACK_API_ORIGIN="$api_origin" QIU_FULLSTACK_MANIFEST="$manifest" \
  npm --prefix "$repo_root/frontend" run test:e2e:full-stack-golden:direct

go_cache="$($go_bin env GOCACHE)"
go_path="$($go_bin env GOPATH)"
go_mod_cache="$($go_bin env GOMODCACHE)"
env -i PATH="$runtime_path" TMPDIR="$run_root/tmp" LC_ALL=C GOCACHE="$go_cache" GOPATH="$go_path" GOMODCACHE="$go_mod_cache" \
  QIU_FULLSTACK_QA=1 QIU_FULLSTACK_MANIFEST="$manifest" "$go_bin" test -count=1 ./fullstack -run '^TestIndependentFullStackEvidence$'
env -i PATH="$runtime_path" TMPDIR="$run_root/tmp" LC_ALL=C GOCACHE="$go_cache" GOPATH="$go_path" GOMODCACHE="$go_mod_cache" \
  QIU_FULLSTACK_QA=1 QIU_FULLSTACK_MANIFEST="$manifest" "$go_bin" test -race -count=1 ./fullstack -run '^TestIndependentFullStackEvidence$'

curl --noproxy '*' --silent --show-error --fail "$api_origin/__full-stack/evidence" >"$run_root/final-evidence.json"
/usr/bin/python3 - "$run_root/final-evidence.json" <<'PY'
import json, sys
e = json.load(open(sys.argv[1], encoding="utf-8"))
final = e["final"]
print("[full-stack-golden] backend_a_pid=%s backend_b_pid=%s pg_head=%s snapshot=%s facts=%s trades=%s ledger_tx=%s ledger_entries=%s orders=%s" % (
    e["backend_a"]["pid"], e["backend_b"]["pid"], e["postgres"]["head_sequence"], e["postgres"]["snapshot_sequence"],
    final["counts"]["facts"], final["counts"]["trades"], final["counts"]["ledger_transactions"], final["counts"]["ledger_entries"], final["counts"]["orders"]))
print("[full-stack-golden] PASS playwright=2 independent_qa=normal+race")
PY
