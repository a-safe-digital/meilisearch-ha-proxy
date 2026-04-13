# MeiliSearch HA Proxy

[![CI](https://github.com/a-safe-digital/meilisearch-ha-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/a-safe-digital/meilisearch-ha-proxy/actions/workflows/ci.yml)
[![Release](https://github.com/a-safe-digital/meilisearch-ha-proxy/actions/workflows/release.yml/badge.svg)](https://github.com/a-safe-digital/meilisearch-ha-proxy/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/a-safe-digital/meilisearch-ha-proxy)](https://goreportcard.com/report/github.com/a-safe-digital/meilisearch-ha-proxy)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

High-availability reverse proxy for [MeiliSearch](https://www.meilisearch.com/) Community Edition. Provides **write replication**, **automatic failover**, and **read load balancing** across multiple MeiliSearch instances — without requiring MeiliSearch Cloud or Enterprise Edition.

## Why

MeiliSearch CE is single-node. If it goes down, search is down. MeiliSearch Cloud/Enterprise offers HA but requires a commercial license (BUSL 1.1). This proxy wraps multiple CE instances (MIT) behind a single endpoint and keeps them in sync.

## Architecture

```
                ┌──────────────┐
  Clients ────▶ │   HA Proxy   │
                │  :7700       │
                └──────┬───────┘
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
    ┌──────────┐ ┌──────────┐ ┌──────────┐
    │ MeiliSearch│ │MeiliSearch│ │MeiliSearch│
    │  Primary  │ │ Replica 1│ │ Replica 2│
    │  :7710    │ │  :7711   │ │  :7712   │
    └──────────┘ └──────────┘ └──────────┘
```

- **Writes** (POST/PUT/PATCH/DELETE) go to the primary and are replicated to all replicas
- **Reads** (GET) are round-robined across all healthy instances
- **Admin** requests (/keys, /dumps, /snapshots) go to the primary only
- **Health checks** continuously monitor all backends with configurable thresholds
- **Failover** automatically promotes a replica if the primary becomes unhealthy

## Features

- **Write replication** — every write is forwarded to all replicas in parallel
- **Read load balancing** — round-robin across healthy backends
- **Automatic failover** — detects primary failure and promotes a replica
- **Health monitoring** — configurable intervals, timeouts, and thresholds
- **Request classification** — smart routing based on HTTP method + path
- **Prometheus metrics** — exposed on configurable metrics port
- **MeiliSearch API compatible** — drop-in replacement, works with any MeiliSearch SDK
- **Multi-arch** — Docker images for `linux/amd64` and `linux/arm64`
- **Helm chart** — deploy to Kubernetes with a single command

## Quick Start

### Docker Compose (recommended)

```bash
git clone https://github.com/a-safe-digital/meilisearch-ha-proxy.git
cd meilisearch-ha-proxy
make docker-up

# Test it
curl -s http://localhost:7700/health | jq
curl -s http://localhost:7700/cluster/status | jq

# Create an index
curl -s -X POST http://localhost:7700/indexes \
  -H 'Authorization: Bearer dev-master-key' \
  -H 'Content-Type: application/json' \
  -d '{"uid": "movies", "primaryKey": "id"}'

# Tear down
make docker-down
```

### Binary

```bash
# Download the latest release
# https://github.com/a-safe-digital/meilisearch-ha-proxy/releases

# Or build from source
make build
./bin/meili-ha-proxy --config configs/meili-ha.yaml
```

### Helm (Kubernetes)

```bash
helm install meilisearch-ha-proxy \
  oci://ghcr.io/a-safe-digital/charts/meilisearch-ha-proxy \
  --version 0.1.0 \
  --set meilisearch.masterKey=your-master-key \
  --namespace search \
  --create-namespace
```

## Configuration

The proxy is configured via a YAML file:

```yaml
# configs/meili-ha.yaml
listen_addr: ":7700"
metrics_addr: ":9090"
api_key: "your-master-key"

backends:
  - url: "http://meili-primary:7700"
    role: primary
  - url: "http://meili-replica-1:7700"
    role: replica
  - url: "http://meili-replica-2:7700"
    role: replica

health_check:
  interval: 5s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2

replication:
  timeout: 30s
  max_payload_size: 200MB
```

Environment variable: `MEILI_HA_CONFIG=/path/to/config.yaml`

## Request Routing

| Method | Path Pattern | Route |
|--------|-------------|-------|
| GET | `/indexes/*`, `/multi-search`, `/health`, `/version`, `/stats` | Round-robin (all healthy) |
| POST/PUT/PATCH/DELETE | `/indexes/*`, `/swap-indexes` | Primary + replicate to all |
| ANY | `/keys/*`, `/dumps`, `/snapshots`, `/cluster/status` | Primary only |

## Endpoints

In addition to proxying all MeiliSearch endpoints, the proxy exposes:

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Proxy health (returns 200 if at least one backend is healthy) |
| `GET /cluster/status` | Status of all backends (primary/replica, healthy/unhealthy) |
| `GET /metrics` (metrics port) | Prometheus metrics |

## Helm Chart

The Helm chart deploys both the proxy and MeiliSearch instances:

```bash
# Install
helm install meilisearch-ha-proxy \
  oci://ghcr.io/a-safe-digital/charts/meilisearch-ha-proxy \
  --version 0.1.0 \
  -f values.yaml

# Upgrade
helm upgrade meilisearch-ha-proxy \
  oci://ghcr.io/a-safe-digital/charts/meilisearch-ha-proxy \
  --version 0.1.0 \
  -f values.yaml
```

See [`charts/meilisearch-ha-proxy/values.yaml`](charts/meilisearch-ha-proxy/values.yaml) for all configurable values.

## Development

```bash
# Prerequisites: Go 1.24+, Docker, golangci-lint

# Build
make build

# Run all tests
make test-all

# Individual test suites
make test-unit          # Unit tests (~83% coverage)
make test-integration   # Integration tests (needs MeiliSearch instances)
make test-e2e           # E2E tests (starts docker-compose automatically)

# Lint
make lint

# Full dev environment
make docker-up          # Starts proxy + 3 MeiliSearch instances
make docker-logs        # Follow logs
make docker-down        # Tear down
```

### Project Structure

```
cmd/meili-ha-proxy/     Entry point
internal/
  config/               Configuration loading
  consensus/            Raft-based leader election (future)
  proxy/                Reverse proxy, classifier, admin endpoints
  health/               Health checker
  replication/          Write replication to replicas
  failover/             Automatic primary failover
charts/                 Helm chart
docker/                 Dockerfiles and compose files
test/
  e2e/                  End-to-end tests (meilisearch-go SDK)
  integration/          Integration tests
  testutil/             Shared test helpers
```

## Container Images

Multi-arch images are published to GitHub Container Registry:

```bash
# Latest from main branch
docker pull ghcr.io/a-safe-digital/meilisearch-ha-proxy:main

# Specific release
docker pull ghcr.io/a-safe-digital/meilisearch-ha-proxy:v0.1.0

# By commit SHA
docker pull ghcr.io/a-safe-digital/meilisearch-ha-proxy:sha-abc1234
```

Supported architectures: `linux/amd64`, `linux/arm64`

## License

[MIT](LICENSE)
