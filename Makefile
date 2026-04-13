BINARY := meili-ha-proxy
PKG := github.com/a-safe-digital/meilisearch-ha-proxy
CMD := ./cmd/meili-ha-proxy

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build build-arm64 test test-unit test-integration test-e2e lint fmt clean docker run help

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
	go test -race -tags=integration -v -timeout 2m ./test/integration/...

test-e2e: docker-test-up
	@echo "Waiting for proxy to be healthy..."
	@for i in $$(seq 1 30); do \
		if curl -sf http://localhost:7700/health > /dev/null 2>&1; then \
			echo "Proxy is healthy!"; \
			break; \
		fi; \
		sleep 2; \
	done
	PROXY_URL=http://localhost:7700 PROXY_API_KEY=test-master-key \
		go test -race -tags=e2e -v -timeout 5m ./test/e2e/... || \
		(docker compose -f docker/docker-compose.test.yml down -v && exit 1)
	docker compose -f docker/docker-compose.test.yml down -v

test-all: test-unit test-integration test-e2e

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
	docker compose -f docker/docker-compose.yml up -d --build
	@echo ""
	@echo "=== MeiliSearch HA Proxy running ==="
	@echo "  Proxy:       http://localhost:7700"
	@echo "  Metrics:     http://localhost:9090"
	@echo "  Primary:     http://localhost:7710"
	@echo "  Replica 1:   http://localhost:7711"
	@echo "  Replica 2:   http://localhost:7712"
	@echo ""
	@echo "  API Key:     dev-master-key"
	@echo ""
	@echo "Try: curl -s http://localhost:7700/health | jq"
	@echo "     curl -s http://localhost:7700/cluster/status | jq"

docker-down:
	docker compose -f docker/docker-compose.yml down -v

docker-test-up:
	docker compose -f docker/docker-compose.test.yml up -d --build --wait --wait-timeout 120

docker-test-down:
	docker compose -f docker/docker-compose.test.yml down -v

docker-logs:
	docker compose -f docker/docker-compose.yml logs -f

## Run (local, requires MeiliSearch backends running)

run: build
	./bin/$(BINARY)

## Clean

clean:
	rm -rf bin/ dist/ coverage.out coverage.html

## Help

help:
	@echo "MeiliSearch HA Proxy"
	@echo ""
	@echo "Usage:"
	@echo "  make build           Build the binary"
	@echo "  make build-arm64     Cross-compile for linux/arm64"
	@echo "  make test            Run unit tests (alias for test-unit)"
	@echo "  make test-unit       Run unit tests with coverage"
	@echo "  make test-integration Run integration tests (needs MeiliSearch)"
	@echo "  make test-e2e        Run E2E tests (starts docker-compose)"
	@echo "  make test-all        Run all test suites"
	@echo "  make lint            Run golangci-lint"
	@echo "  make docker          Build Docker image"
	@echo "  make docker-up       Start full dev stack (proxy + 3 MeiliSearch)"
	@echo "  make docker-down     Stop dev stack"
	@echo "  make docker-logs     Follow dev stack logs"
	@echo "  make all             Lint + test + build"
	@echo "  make clean           Remove build artifacts"
	@echo "  make help            Show this help"
