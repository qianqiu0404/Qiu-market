#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
mode=${1:-all}
verify_db_name=''
created_verify_db=false

cleanup_verify_db() {
	if [ "$created_verify_db" = true ] && [ -n "$verify_db_name" ]; then
		dropdb --force --if-exists "$verify_db_name"
		created_verify_db=false
	fi
}

trap cleanup_verify_db 0
trap 'exit 130' 2
trap 'exit 143' 15

prepare_postgres() {
	if [ -n "${S78_TEST_POSTGRES_DSN:-}" ]; then
		return
	fi
	for required_command in psql createdb dropdb; do
		if ! command -v "$required_command" >/dev/null 2>&1; then
			echo "$required_command is required for PostgreSQL integration tests" >&2
			exit 1
		fi
	done
	verify_db_name="s78_trading_verify_$$"
	if [ "$(psql -d postgres -Atqc "select count(*) from pg_database where datname='$verify_db_name'")" != 0 ]; then
		echo "refusing to reuse database $verify_db_name" >&2
		exit 1
	fi
	createdb "$verify_db_name"
	created_verify_db=true
	S78_TEST_POSTGRES_DSN="postgresql:///$verify_db_name"
	S78_TEST_POSTGRES_ISOLATED=1
	export S78_TEST_POSTGRES_DSN
	export S78_TEST_POSTGRES_ISOLATED
}

verify_postgres() {
	prepare_postgres
	cd "$repo_root"
	go test -count=1 -p 1 -v \
		./trading/auth \
		./trading/store/postgres \
		./trading/e2e \
		./trading/integration
	go test -race -count=1 -p 1 ./trading/e2e ./trading/integration
}

verify_npm_audit() {
	audit_attempt=1
	while ! npm_config_fetch_retries=3 \
		npm_config_fetch_retry_mintimeout=1000 \
		npm_config_fetch_retry_maxtimeout=5000 \
		npm audit --audit-level=high; do
		if [ "$audit_attempt" -ge 3 ]; then
			echo "npm audit failed after $audit_attempt attempts" >&2
			return 1
		fi
		echo "npm audit attempt $audit_attempt failed; retrying" >&2
		sleep "$audit_attempt"
		audit_attempt=$((audit_attempt + 1))
	done
}

verify_web() {
	cd "$repo_root/trading/web"
	npm test
	npm run build
	verify_npm_audit
}

if [ "$mode" = postgres ]; then
	verify_postgres
	exit 0
fi

if [ "$mode" = web ]; then
	verify_web
	exit 0
fi

if [ "$mode" != all ]; then
	echo "usage: $0 [postgres|web]" >&2
	exit 2
fi

cd "$repo_root"
unformatted=$(find trading -type f -name '*.go' -print0 | xargs -0 gofmt -l)
if [ -n "$unformatted" ]; then
	echo "unformatted Go files:" >&2
	echo "$unformatted" >&2
	exit 1
fi

go test ./...
go test -race ./trading/...
go vet ./trading/...
verify_postgres
GOMAXPROCS=2 go test ./trading/exchange -run '^$' -fuzz '^FuzzExchange$' -fuzztime "${S78_TRADING_FUZZ_TIME:-10s}"
go test ./trading/orderbook -run '^$' -bench '^BenchmarkMatch$' -benchmem
verify_web

cd "$repo_root"
git diff --check
