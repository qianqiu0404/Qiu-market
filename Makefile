GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')
RELEASE_REV ?= HEAD

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitData=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

market-services:
	go mod tidy
	env GO111MODULE=on go build -v $(LDFLAGS) ./cmd/market-services

dev-deps:
	docker compose up -d postgres redis

doris-deps:
	docker compose up -d doris

migrate: market-services
	. ./.env; ./market-services migrate

seed:
	. ./.env; psql -h "$$MARKET_MASTER_DB_HOST" -p "$$MARKET_MASTER_DB_PORT" -U "$$MARKET_MASTER_DB_USER" -d "$$MARKET_MASTER_DB_NAME" -f script/seed-dashboard.sql

api: market-services
	. ./.env; ./market-services api

trading: market-services
	. ./.env; ./market-services trading

rpc: market-services
	. ./.env; ./market-services rpc

crawler: market-services
	. ./.env; ./market-services crawler

dex: market-services
	. ./.env; ./market-services dex

worker: market-services
	. ./.env; ./market-services worker

dw: market-services
	. ./.env; ./market-services dw

frontend-dev:
	cd frontend && npm run dev

frontend-build:
	cd frontend && npm run build

dev:
	bash script/dev.sh up

dev-status:
	bash script/dev.sh status

dev-stop:
	bash script/dev.sh stop

dev-logs:
	bash script/dev.sh logs

dev-restart:
	@test -n "$(ROLE)" || (echo "Usage: make dev-restart ROLE=crawler" >&2; exit 2)
	bash script/dev.sh restart "$(ROLE)"

verify-local:
	bash script/verify-local.sh

verify-trading-golden:
	cd frontend && npm ci && npm run test:e2e:golden

repo-audit:
	bash script/repo-audit.sh

mac-production-build:
	bash ops/macos/manage-release-candidate.sh prepare "$(RELEASE_REV)"

mac-production-verify:
	bash ops/macos/manage-release-candidate.sh verify "$(RELEASE_REV)"

mac-production-preflight:
	bash ops/macos/manage-release-candidate.sh preflight "$(RELEASE_REV)"

mac-production-install:
	bash ops/macos/manage-services.sh install

mac-production-status:
	bash ops/macos/manage-services.sh status

clean:
	rm market-services

test:
	go test -v ./...

lint:
	golangci-lint run ./...

proto:
	sh ./script/compile.sh

.PHONY: \
	market-services \
	dev-deps \
	doris-deps \
	migrate \
	seed \
	api \
	trading \
	rpc \
	crawler \
	dex \
	worker \
	dw \
	frontend-dev \
	frontend-build \
	dev \
	dev-status \
	dev-stop \
	dev-logs \
	dev-restart \
	verify-local \
	verify-trading-golden \
	repo-audit \
	mac-production-build \
	mac-production-verify \
	mac-production-preflight \
	mac-production-install \
	mac-production-status \
	clean \
	test \
	lint \
	proto
