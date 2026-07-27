#!/usr/bin/env bash
set -euo pipefail

failed=0
if health="$(curl --fail --silent --show-error --max-time 3 http://127.0.0.1:9092/healthz)"; then
  echo "api health: ${health:-ok}"
else
  echo "api health: unavailable" >&2
  failed=1
fi
if nc -z 127.0.0.1 9094 >/dev/null 2>&1; then
  echo "trading gRPC: listening"
else
  echo "trading gRPC: unavailable" >&2
  failed=1
fi
for dependency in postgresql@16 redis; do
  if brew services list 2>/dev/null | awk -v name="$dependency" '$1 == name && $2 == "started" { found=1 } END { exit !found }'; then
    echo "$dependency: started"
  else
    echo "$dependency: not started" >&2
    failed=1
  fi
done
exit "$failed"
