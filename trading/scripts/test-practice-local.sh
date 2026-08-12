#!/bin/bash
set -euo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d /tmp/qiu-market-t1-practice.XXXXXX)"
runtime="$fixture/runtime"
cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  for role in gateway trading; do
    [ ! -f "$runtime/run/$role.pid" ] || kill -TERM "$(<"$runtime/run/$role.pid")" 2>/dev/null || true
  done
  find "$fixture" -depth -delete 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM HUP
install -d -m 700 "$runtime/config" "$runtime/secrets" "$runtime/migrations"
printf '%s\n' 'postgres://fixture@127.0.0.1/state' > "$runtime/config/state-postgres-dsn"
printf '%s\n' 'postgres://fixture@127.0.0.1/reference' > "$runtime/config/reference-postgres-dsn"
printf '%s\n' 'fixture:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' > "$runtime/secrets/cursor-hmac-current"
chmod 600 "$runtime/config/"* "$runtime/secrets/"*

fake="$fixture/market-services-fixture"
cat > "$fake" <<'FAKE'
#!/bin/bash
set -euo pipefail
[ "$MARKET_TRADING_PRACTICE_MODE" = true ]
[ "$MARKET_TRADING_LOCAL_AUTH" = true ]
[ "$MARKET_TRADING_SECURE_COOKIES" = false ]
[ "$MARKET_TRADING_GRPC_ADDR" = 127.0.0.1:19094 ]
[ "$MARKET_TRADING_ALLOWED_ORIGINS" = http://127.0.0.1:15174 ]
[ -f "$MARKET_TRADING_STATE_DSN_FILE" ]
[ -f "$MARKET_TRADING_REFERENCE_DSN_FILE" ]
child=''
if [ "$1" = trading-gateway ]; then
  /usr/bin/python3 - <<'PY' &
from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.path in (
            '/healthz', '/api/v1/trading/markets/BTC-USDT/status'
        ) else 404)
        self.end_headers()
    def log_message(self, *args):
        pass
HTTPServer(('127.0.0.1', 19092), Handler).serve_forever()
PY
  child=$!
else
  [ "$1" = trading ]
  /usr/bin/python3 - <<'PY' &
import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', 19094))
s.listen()
while True:
    connection, _ = s.accept()
    connection.close()
PY
  child=$!
fi
trap '[ -z "$child" ] || kill "$child" 2>/dev/null || true; exit 0' TERM INT
while :; do sleep 1; done
FAKE
chmod 700 "$fake"

manager="$repo_root/trading/scripts/practice-local.sh"
export QIU_T1_FRONTEND_ORIGIN='http://127.0.0.1:15174'
"$manager" start "$runtime" "$fake" >/dev/null
"$manager" status "$runtime" | grep -E '^practice stack running trading_pid=[1-9][0-9]* gateway_pid=[1-9][0-9]* http=127.0.0.1:19092 grpc=127.0.0.1:19094$' >/dev/null
"$manager" stop "$runtime" >/dev/null
for role in trading gateway; do
  [ ! -e "$runtime/run/$role.pid" ] && [ ! -e "$runtime/run/$role.owner" ]
done

for unsafe_origin in \
  'https://127.0.0.1:15174' \
  'http://localhost:15174' \
  'http://127.0.0.1' \
  'http://127.0.0.1:9092' \
  'http://127.0.0.1:70000'; do
  if QIU_T1_FRONTEND_ORIGIN="$unsafe_origin" "$manager" start "$runtime" "$fake" >/dev/null 2>&1; then
    echo "unsafe practice frontend origin was accepted" >&2
    exit 1
  fi
done

chmod 644 "$runtime/config/reference-postgres-dsn"
if "$manager" start "$runtime" "$fake" >/dev/null 2>&1; then
  echo 'world-readable reference DSN file was accepted' >&2
  exit 1
fi
chmod 600 "$runtime/config/reference-postgres-dsn"
mv "$runtime/config/reference-postgres-dsn" "$runtime/config/reference-postgres-dsn.target"
ln -s "$runtime/config/reference-postgres-dsn.target" "$runtime/config/reference-postgres-dsn"
if "$manager" start "$runtime" "$fake" >/dev/null 2>&1; then
  echo 'symlink reference DSN file was accepted' >&2
  exit 1
fi

fake_live="$fixture/d1-candidate"
install -d -m 700 "$fake_live"
if "$manager" start "$fake_live" "$fake" >/dev/null 2>&1; then
  echo 'live runtime was accepted as a practice target' >&2
  exit 1
fi

echo 'Practice local start, ownership, cleanup, and private-file fixtures passed.'
