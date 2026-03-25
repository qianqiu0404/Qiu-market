GITCOMMIT := $(shell git rev-parse HEAD)
GITDATE := $(shell git show -s --format='%ct')

LDFLAGSSTRING +=-X main.GitCommit=$(GITCOMMIT)
LDFLAGSSTRING +=-X main.GitDate=$(GITDATE)
LDFLAGS := -ldflags "$(LDFLAGSSTRING)"

market-services:
	go mod tidy
	env GO111MODULE=on go build -v $(LDFLAGS) ./cmd/market-services

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
	clean \
	test \
	lint \
	proto