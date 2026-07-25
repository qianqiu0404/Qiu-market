.PHONY: verify-local verify-go verify-postgres verify-fuzz verify-web

verify-local:
	./scripts/verify-local.sh

verify-go:
	cd .. && go test ./... && go test -race ./trading/... && go vet ./trading/...

verify-postgres:
	./scripts/verify-local.sh postgres

verify-fuzz:
	cd .. && GOMAXPROCS=2 go test ./trading/exchange -run '^$$' -fuzz '^FuzzExchange$$' -fuzztime 10s
	cd .. && go test ./trading/orderbook -run '^$$' -bench '^BenchmarkMatch$$' -benchmem

verify-web:
	cd web && npm test && npm run build && npm audit --audit-level=high
