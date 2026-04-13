BINARY := meili-ha-proxy
PKG := github.com/a-safe-digital/meilisearch-ha-proxy
CMD := ./cmd/meili-ha-proxy

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build build-arm64 test test-unit test-integration test-e2e lint fmt clean docker run

all: lint test build

## Build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) $(CMD)

build-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64 $(CMD)

## Test

test: test-unit

test-unit:
	go test -race -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | tail -1

test-integration:
	go test -race -tags=integration -v ./test/integration/...

test-e2e:
	docker compose -f docker/docker-compose.test.yml up -d --wait
	go test -race -tags=e2e -v ./test/e2e/... || (docker compose -f docker/docker-compose.test.yml down && exit 1)
	docker compose -f docker/docker-compose.test.yml down

test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## Lint

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .
	goimports -w .

## Docker

docker:
	docker build --platform linux/arm64 -f docker/Dockerfile -t $(BINARY) .

docker-up:
	docker compose -f docker/docker-compose.yml up -d

docker-down:
	docker compose -f docker/docker-compose.yml down

## Run

run: build
	./bin/$(BINARY)

## Clean

clean:
	rm -rf bin/ dist/ coverage.out coverage.html
