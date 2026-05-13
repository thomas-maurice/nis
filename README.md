# NATS Identity Service (NIS)

Centralized JWT authentication management for NATS servers.

## What It Does

NIS manages the complete lifecycle of NATS JWT authentication:

- **Creates & signs JWTs** - Operators, accounts, users with Ed25519 keys
- **Encrypts credentials** - All private keys encrypted at rest (ChaCha20-Poly1305)
- **Syncs to NATS** - Push account JWTs dynamically via `$SYS.REQ.CLAIMS.UPDATE`
- **Monitors clusters** - Health checks for registered NATS servers
- **Provides interfaces** - Web UI, CLI (`nisctl`), and gRPC API

## Quick Start

### One-command dev stack (`make run`)

The fastest path from a clean checkout to a working stack. Requires Docker and Go 1.25+.

```bash
make run
# Builds NIS, starts Postgres + NATS in Docker, runs NIS on the host pointing at
# both, creates an admin user. UI at http://localhost:8080 (admin / admin123).

make run-demo
# Same as above, plus: creates a demo operator, restarts NATS with JWT auth on,
# registers a demo cluster/account/user, syncs JWTs, and writes a credentials
# file to .run/app-user.creds. Verify NATS works:
nats --creds=.run/app-user.creds --server=nats://localhost:4222 rtt
```

Lifecycle:

```bash
make run-status   # show what's running
make run-logs     # tail NIS server log (run-logs-nats / run-logs-pg for the containers)
make run-stop     # stop server + remove containers (keeps Postgres data volume)
make run-clean    # full wipe (containers + Postgres volume + ./.run/)
```

Local state (pid file, server log, generated NATS config, resolver/jetstream dirs, creds) lives in `./.run/` and is gitignored. Postgres data lives in the Docker volume `nis_dev_pg_data`. Override defaults via env: `RUN_PG_PORT`, `RUN_PG_PASS`, `RUN_JWT_SECRET`, `RUN_ENC_KEY` (see `Makefile`).

### Docker Compose (all-in-Docker, SQLite)

```bash
# Start all services (NIS + NATS, SQLite-backed)
docker-compose up -d

# Access UI at http://localhost:8080
# Login: admin/admin123 (created by the nis-setup container)

# Stop all services
docker-compose down
```

For a Postgres-backed compose stack, use `example/docker-compose.yml`.

### Binary

```bash
./nis serve --jwt-secret "min-32-bytes" --encryption-key "exactly-32-bytes"
./nis user create admin --password admin123 --role admin
./nisctl login http://localhost:8080 -u admin -p admin123
./nisctl operator create my-operator
./nisctl account create my-account --operator my-operator
./nisctl user create my-user --operator my-operator --account my-account
```

### Example Setup (Demo)

```bash
# Quick demo with example scripts
cd example && ./setup.sh
open http://localhost:8080  # Login: admin/admin123
```

## How It Works

```
Operator JWT (root of trust)
  └─ $SYS Account (cluster management)
       └─ System User (credentials)
  └─ App Accounts (multi-tenant isolation)
       └─ Users (application credentials)
            └─ .creds files (exported for NATS clients)
```

1. NIS generates Ed25519 keys and signs JWTs
2. NATS loads operator JWT (trusts this key)
3. Account JWTs pushed to NATS resolver
4. Users connect with credentials signed by their account

## Key Features

- **Scoped Signing Keys** - Delegated JWT signing with pub/sub permissions
- **JetStream Limits** - Per-account memory/storage quotas
- **Role-Based Access** - Admin, operator-admin, account-admin roles
- **Multi-Database** - SQLite (dev) or PostgreSQL (prod)
- **Dark Mode UI** - Responsive Vue.js interface

## Components

- **Server** - Go gRPC service with embedded UI
- **nisctl** - CLI for automation and CI/CD
- **Web UI** - Vue 3 dashboard for visual management

## Configuration

Via config file (`config.yaml`):

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  driver: "sqlite"
  path: "./nis.db"

encryption:
  current_key_id: "default"
  keys:
    - id: "default"
      key: "base64-encoded-32-byte-key"

auth:
  jwt_secret: "your-secret"
  token_expiry: "24h"
```

Via flags:

```bash
./nis serve \
  --address :8080 \
  --db-dsn ./nis.db \
  --jwt-secret "your-secret" \
  --encryption-key "your-key"
```

## Use Cases

**Multi-tenant SaaS** - Isolate customers with separate accounts
**Microservices** - Per-service credentials with scoped permissions
**Development** - Quickly provision test credentials
**Production** - Centralized credential management with encryption

## Build

```bash
make build-all    # Server + CLI + UI
make build-ui     # UI only
make test         # Unit + integration tests
make test-e2e     # End-to-end suite (boots NIS + real NATS in Docker, asserts permissions)
```

`make test-e2e` is the regression net for refactors of the server, services, NATS plumbing, persistence, or encryption layers. CI runs it on every PR; run it locally after non-trivial server-side changes. Requires the Docker daemon for the NATS container; uses random TCP ports so it can run alongside `make run`.

Convenience targets:

| Target | What it does |
|---|---|
| `make run` | Full dev stack: Postgres + NATS (Docker) + NIS (host) + admin user |
| `make run-demo` | `run` + JWT bootstrap (operator, demo cluster/account/user, creds file) |
| `make run-stop` / `run-clean` | Stop / wipe the dev stack |
| `make serve-local` | Legacy host-only server, SQLite, hardcoded dev secrets |
| `make docker-build` / `docker-run` / `docker-stop` | Single-container Docker image lifecycle |

## Observability

NIS exports Prometheus metrics, OpenTelemetry traces, and three HTTP probe
endpoints. Everything except trace export is on by default — scraping
`/metrics` works out of the box.

### Endpoints

| Path | Status | What it tells you |
|---|---|---|
| `GET /livez` | 200 | Process is alive. Use for k8s liveness probes. |
| `GET /healthz` | 200 / 503 | Migrations have run. **Back-compat** with the existing Dockerfile HEALTHCHECK and docker-compose; intentionally lax so a transient DB blip won't restart containers. |
| `GET /readyz` | 200 / 503, JSON | Strict: migrations + DB ping + encryption self-test. Use for k8s readiness probes and Prometheus blackbox checks. Body lists each component. |
| `GET /metrics` | 200 | Prometheus scrape endpoint (OpenMetrics format). |

Quick check:

```bash
curl -s http://localhost:8080/livez                  # ok
curl -s http://localhost:8080/healthz                # ok
curl -s http://localhost:8080/readyz | jq            # {"status":"ok","components":{"database":"ok",...}}
curl -s http://localhost:8080/metrics | head -40
```

### Prometheus metrics

The interesting series:

| Metric | Type | Labels | What it measures |
|---|---|---|---|
| `rpc_server_duration_milliseconds` | histogram | `rpc_service`, `rpc_method`, `rpc_grpc_status_code` | Connect-RPC request latency. Emitted by `otelconnect` using OpenTelemetry semantic conventions. |
| `nis_http_server_duration_seconds` | histogram | `path_class`, `method`, `status` | Non-RPC HTTP request latency. `path_class` is bucketed (`ui`/`other`/…) to bound cardinality. |
| `nis_operators_total`, `nis_accounts_total`, `nis_users_total`, `nis_scoped_keys_total`, `nis_clusters_total` | gauge | — | Entity inventory. Refreshed every 60s, served from an in-memory cache (no live `COUNT(*)` per scrape). |
| `nis_clusters_healthy` | gauge | — | Clusters last reported healthy by the 60s health-check loop. |
| `nis_cluster_sync_duration_seconds` | histogram | `outcome` | Duration of `SyncCluster` operations. `outcome` is `ok` / `err`. |
| `nis_cluster_sync_errors_total` | counter | `phase` | Sync errors broken down by where they happened (`open_cluster`, `list_accounts`, …). |
| `nis_cluster_health_check_failures_total` | counter | — | 60s loop saw a cluster fail to connect or lack credentials. |
| `nis_encryption_failures_total` | counter | `op` | `op` is `encrypt` / `decrypt`. A decrypt-failure spike usually means a key-rotation problem — alert on this. |
| `nis_auth_rejections_total` | counter | `reason` | RPC rejected by the auth interceptor. `reason` ∈ `missing_token`, `invalid_token`, `forbidden`. |

Plus the standard `go_*` and `process_*` collectors (heap, goroutines, FDs, GC).

Scrape it from Prometheus:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: nis
    static_configs:
      - targets: ['nis:8080']
    metrics_path: /metrics
```

Suggested alerts and example rules are in [OPERATIONS.md](OPERATIONS.md#monitoring).

### Distributed tracing (OpenTelemetry)

**Off by default.** When you enable it, NIS exports spans for every RPC and
non-probe HTTP request over OTLP/gRPC. No code changes needed in your
collector — `connectrpc.com/otelconnect` and `otelhttp` produce standard OTel
semantic-convention spans, so any OTLP-compatible backend (Jaeger, Tempo,
Honeycomb, Datadog, …) will render them.

**Quickest path: Jaeger all-in-one**

```bash
# 1. Run Jaeger locally. OTLP/gRPC on 4317, UI on 16686.
docker run -d --name jaeger \
  -p 4317:4317 -p 16686:16686 \
  jaegertracing/all-in-one:latest

# 2. Run NIS with tracing on, pointed at the local collector.
./bin/nis serve \
  --tracing-enabled \
  --tracing-endpoint localhost:4317 \
  --tracing-insecure \
  --jwt-secret "..." --encryption-key "..."

# 3. Drive some traffic through nisctl or the UI, then open Jaeger.
open http://localhost:16686
# Select service "nis" → click Find Traces.
```

**Configuration**

Tracing options can be set via flag, env var (`NIS_TRACING_*` once viper
prefix is in play, or use the documented `--tracing-*` flags), or
`config.yaml`:

| Flag | Default | Notes |
|---|---|---|
| `--tracing-enabled` | `false` | Master switch. When off, the SDK is fully no-op — zero overhead. |
| `--tracing-endpoint` | `localhost:4317` | OTLP/gRPC `host:port`. |
| `--tracing-insecure` | `true` | Disables TLS for the collector connection — fine for sidecar / loopback collectors. Set to `false` when crossing untrusted networks. |
| `--tracing-sample-ratio` | `1.0` | Parent-based TraceIDRatio sampler. `1.0` = sample every trace; lower it (e.g. `0.1`) in production if you have heavy traffic. |
| `--tracing-service-name` | `nis` | Becomes the `service.name` resource attribute. |

```yaml
# config.yaml
tracing:
  enabled: true
  endpoint: "otel-collector.observability.svc:4317"
  insecure: false
  sample_ratio: 0.1
  service_name: "nis-prod"
```

**What you'll see**

Each top-level RPC gets a span named after the procedure (e.g.
`nis.v1.OperatorService/CreateOperator`) with the request/response size,
duration, and Connect status code. The Connect interceptor and the HTTP
middleware are both wired up, so requests are traced end-to-end including
the auth interceptor. Calls into the database (GORM) are **not** auto-traced
today — that's a follow-up.

**Disabling per-environment**

The cleanest "off" is `--tracing-enabled=false`. If you set the standard
OTel env var `OTEL_SDK_DISABLED=true`, the SDK shorts everything to no-op
regardless of the flag.

### Metrics-only deployment

If you don't want OpenTelemetry at all, leave `--tracing-enabled=false`
(the default) and just scrape `/metrics` — the meter provider uses the OTel
Prometheus exporter, so no collector is required for metrics.

## Production

- Use PostgreSQL for database
- Enable HTTPS (reverse proxy)
- Rotate encryption keys
- Regular backups
- Multiple instances behind load balancer
- Scrape `/metrics` and probe `/readyz` (not `/healthz`) for accurate readiness
- Point an OTLP collector at NIS for traces if you want request-level visibility

## Links

- **Quickstart**: [QUICKSTART.md](QUICKSTART.md)
- **Dev Guide**: [CLAUDE.md](CLAUDE.md)
- **Docker Hub**: https://hub.docker.com/r/mauricethomas/nis
- **NATS JWT Docs**: https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt

## License

MIT
