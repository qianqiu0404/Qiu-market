GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitData=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

market-services:
	go mod tidy
	env GO111MODULE=on go build -v $(LDFLAGS) ./cmd/market-services

dev-deps:
	docker compose up -d postgres redis

migrate: market-services
	. ./.env; ./market-services migrate

seed:
	. ./.env; psql -h "$$MARKET_MASTER_DB_HOST" -p "$$MARKET_MASTER_DB_PORT" -U "$$MARKET_MASTER_DB_USER" -d "$$MARKET_MASTER_DB_NAME" -f script/seed-dashboard.sql

api: market-services
	. ./.env; ./market-services api

crawler: market-services
	. ./.env; ./market-services crawler

worker: market-services
	. ./.env; ./market-services worker

frontend-dev:
	cd frontend && npm run dev

frontend-build:
	cd frontend && npm run build

verify-local:
	bash script/verify-local.sh

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
	migrate \
	seed \
	api \
	crawler \
	worker \
	frontend-dev \
	frontend-build \
	verify-local \
	clean \
	test \
	lint \
	proto
