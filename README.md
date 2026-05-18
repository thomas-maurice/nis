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
- **Audit Log & Webhooks** - Every mutation recorded; HMAC-signed HTTP notifications
- **Multi-Database** - SQLite (dev) or PostgreSQL (prod)
- **Dark Mode UI** - Responsive Vue.js interface

## Components

- **Server** - Go gRPC service with embedded UI
- **nisctl** - CLI for automation and CI/CD
- **Web UI** - Vue 3 dashboard for visual management

## Configuration

Precedence (high → low): **explicit flag > env var > config file > built-in default.** See `config.example.yaml` for a fully-annotated reference of every key the server reads.

Via config file (`config.yaml`):

```yaml
server:
  address: ":8080"
  enable_ui: true

database:
  driver: "sqlite"
  dsn: "./nis.db"            # filesystem path for sqlite, libpq DSN for postgres
  auto_migrate: true

encryption:
  current_key_id: "default"
  keys:
    - id: "default"
      key: "base64-encoded-32-byte-key"

auth:
  jwt_secret: "min-32-bytes"
  jwt_ttl: "24h"
```

Via flags:

```bash
./nis serve \
  --address :8080 \
  --db-dsn ./nis.db \
  --jwt-secret "your-secret" \
  --encryption-key "your-key"
```

Via env vars (dot → underscore, uppercased):

```bash
DATABASE_DSN="host=localhost port=5432 user=nis password=... dbname=nis sslmode=disable" \
DATABASE_DRIVER=postgres \
AUTH_JWT_SECRET="min-32-bytes" \
ENCRYPTION_KEY="exactly-32-bytes-..............." \
./nis serve
```

## Use Cases

**Multi-tenant SaaS** - Isolate customers with separate accounts
**Microservices** - Per-service credentials with scoped permissions
**Development** - Quickly provision test credentials
**Production** - Centralized credential management with encryption

## Export & Import

Operators (and everything underneath — accounts, users, scoped signing keys, clusters) can be backed up to and restored from a single file. Both **YAML** (default) and **JSON** are supported. Every export carries seed material — there is no "metadata-only" mode, because an export without seeds isn't restorable (the JWT's baked-in public key cannot be reproduced from a regenerated NKey pair).

```bash
# Default: seeds stay encrypted with the server's current encryption key.
# Importable only by a server that uses the same key.
nisctl export operator my-operator -o backup.yaml
nisctl export operator my-operator --format json -o backup.json

# Disaster-recovery: decrypt seeds and emit them as plaintext NKey seeds.
# Importable by ANY server (the import re-encrypts with the destination's
# current key). Use this when you need a backup that survives encryption-key
# loss or rotation.
#
# DANGER: the resulting file is a plaintext NKey vault — protect it like a
# .creds file. Anyone with read access can mint credentials for every entity.
nisctl export operator my-operator --plaintext-secrets -o backup-dr.yaml

# Import. Format is auto-detected from the file contents — no flag needed,
# the same command handles either encoding, encrypted or plaintext.
nisctl export import backup.yaml
nisctl export import backup-dr.yaml

# Restore over an existing operator (same operator ID). Replaces the operator's
# subtree — accounts, users, scoped signing keys — atomically. Attached
# clusters are PRESERVED (they model live NATS infrastructure tied to the
# operator JWT, and re-importing them from a stale backup would clobber
# running state). Without --overwrite, importing over an existing operator
# ID is refused, since silently truncating accounts/users created since the
# export is a footgun.
nisctl export import backup.yaml --overwrite
```

Imports are atomic: the whole flow runs in a single database transaction, so a mid-import failure rolls every partial write back instead of leaving orphan accounts behind. The same applies to `nisctl export import-nsc <archive> <operator-name>`, which ingests a tar/zip of an existing `~/.nsc/stores` tree (the other direction — migrating off `nsc`).

**Deleting an operator** is refused while clusters are still attached to it. `clusters.operator_id` is `ON DELETE RESTRICT` because clusters model live NATS servers configured with the operator's JWT — a silent cascade would lose track of running infrastructure. Delete (or detach by deleting) every attached cluster first, then the operator delete will succeed. The error message names the offending clusters so you know which to clean up.

## Events & Webhooks

Every mutation through NIS (create/update/delete on operators, accounts, users, scoped keys, clusters; cluster sync; cluster health transitions) is appended to a durable **events table** in the same transaction as the state change. Operators can subscribe HTTP endpoints to receive HMAC-signed POSTs when events fire. Used for audit trails, Slack/PagerDuty notifications, downstream cache invalidation.

### Event types

| Type | Emitted on |
|---|---|
| `operator.created` / `operator.updated` / `operator.deleted` | Operator lifecycle |
| `account.created` / `account.updated` / `account.deleted` | Account lifecycle (incl. JetStream limit changes) |
| `user.created` / `user.updated` / `user.deleted` | User lifecycle |
| `scoped_key.created` / `scoped_key.updated` / `scoped_key.deleted` | Signing-key lifecycle |
| `cluster.created` / `cluster.updated` / `cluster.deleted` | Cluster lifecycle |
| `cluster.synced` / `cluster.sync_failed` | Each `SyncCluster` call |
| `cluster.health_changed` | The 60s probe sees a healthy→unhealthy or unhealthy→healthy transition |
| `webhook.test` | Operator clicks "Send Test" on a subscription |
| `api_token.created` / `api_token.revoked` | Service-account API token lifecycle |

Events carry `actor_type` (`user` for RPC-driven events from human logins, `api_token` for RPCs driven by a service-account token, `system` for background ones), `actor_id` (the API user OR the token ID, depending on actor_type), `operator_id` / `account_id` scope, `resource_type` + `resource_id`, and a free-form JSON `payload` with event-specific detail.

The events table is **append-only**. Retention defaults to 30 days, configurable via `--events-retention-days` / `EVENTS_RETENTION_DAYS`. The audit log survives the deletion of the resources it references (operator_id is a soft scope, not an FK). FK CASCADE deletions inside the database (e.g. an operator delete that cascade-removes its $SYS account) emit only the top-level event — `operator.deleted` — not one event per cascaded row.

### Browsing the log

- UI: **Events** page (admin-only). Filter by type, operator, time window. Click a row for the full JSON payload.
- CLI: `nisctl event list [--type ...] [--operator ...] [--since 24h] [--limit 50] [--cursor ...]`, `nisctl event get <id>`.
- RPC: `EventService.ListEvents`, `EventService.GetEvent` (admin-only in v1; per-operator scoping is a v1.1 follow-up).

### Webhook subscriptions

Subscriptions are **operator-scoped**: an operator-admin creates and manages subscriptions for their own operator; admins can manage any operator's. The event-type filter is a list of strings (`["account.created", "user.deleted"]`); `["*"]` matches all events and is **admin-only**.

```bash
# Create a subscription
nisctl webhook create \
  --operator my-operator \
  --name slack-acct-watcher \
  --url https://hooks.slack.com/services/T.../B.../X \
  --event-types account.created,account.deleted
# ⇒ prints the HMAC secret ONCE. Save it.

# List
nisctl webhook list [--operator my-operator]

# Trigger a one-shot test delivery
nisctl webhook test <subscription-id>

# See delivery history
nisctl webhook deliveries <subscription-id>
```

UI: **Webhooks** page → "Create webhook" reveals the plaintext secret in a one-time modal; **Send Test** dispatches a `webhook.test` event scoped to the subscription so you can verify your receiver.

### Delivery semantics

- **At-least-once.** A delivery may be POSTed more than once; receivers must dedupe on `X-NIS-Delivery` (per-attempt unique) or `X-NIS-Event` (per-event unique).
- **Retries.** A non-2xx response (or transport error) is retried with exponential backoff (base 10s, ×2, ±20% jitter, capped at 10min). After 5 failed attempts the delivery moves to `dead_letter` and stays in the history without further retries. The operator can re-enable a subscription that the worker auto-disabled (e.g. after encryption-key rotation broke the secret); the next future event will fan out as normal.
- **Order is not guaranteed.** Two events for the same subscription may arrive out of order; use `occurred_at` from the body to sort.

### Webhook HTTP contract

Every POST carries `Content-Type: application/json` and these headers:

| Header | Value |
|---|---|
| `X-NIS-Event` | Event type, e.g. `account.created` |
| `X-NIS-Delivery` | Per-attempt UUID. Use for idempotency. |
| `X-NIS-Subscription` | The subscription ID |
| `X-NIS-Timestamp` | Unix seconds at dispatch time |
| `X-NIS-Signature` | `sha256=<hex>` HMAC-SHA256 of `timestamp.body` using the shared secret |

The body shape:

```json
{
  "id": "evt-uuid",
  "type": "account.created",
  "occurred_at": "2026-05-14T10:30:00Z",
  "actor_type": "user",
  "actor_id": "api-user-uuid",
  "operator_id": "op-uuid",
  "account_id": "acct-uuid",
  "resource_type": "account",
  "resource_id": "acct-uuid",
  "payload": { "name": "app-account", "public_key": "AB..." }
}
```

### Verifying signatures (Go SDK)

The `pkg/webhooks` package ships a verification helper. Import it in your receiver to avoid hand-rolling HMAC:

```go
import "github.com/thomas-maurice/nis/pkg/webhooks"

func main() {
    secret := []byte(os.Getenv("NIS_WEBHOOK_SECRET")) // the secret returned at create time
    http.HandleFunc("/webhooks/nis", func(w http.ResponseWriter, r *http.Request) {
        body, err := webhooks.Verify(r, secret, 0) // 0 = default 5min tolerance
        if err != nil {
            http.Error(w, err.Error(), http.StatusUnauthorized)
            return
        }
        defer body.Close()

        var evt struct {
            Type    string          `json:"type"`
            Payload json.RawMessage `json:"payload"`
        }
        if err := json.NewDecoder(body).Decode(&evt); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        log.Printf("got %s: %s", evt.Type, evt.Payload)
        w.WriteHeader(http.StatusOK)
    })
    log.Fatal(http.ListenAndServe(":8081", nil))
}
```

`webhooks.Verify` returns typed errors so you can distinguish replay attempts (`ErrTimestampSkew`), bad signatures (`ErrSignatureMismatch`), and missing/malformed headers. Check with `errors.Is`.

### Verifying signatures (any language)

The signature is `sha256=` + hex(HMAC-SHA256(secret, timestamp + "." + body)). Pseudocode:

```python
import hmac, hashlib
expected = "sha256=" + hmac.new(secret, (timestamp + "." + body).encode(), hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, sig_header):
    raise Unauthorized()
```

Reject deliveries whose `X-NIS-Timestamp` is more than ~5 minutes off your clock to prevent replay.

### Operational knobs

| Flag / env / config key | Default | What it controls |
|---|---|---|
| `--events-retention-days` / `events.retention_days` | 30 | How long audit-log rows are kept; older rows are swept every 24h |
| `--webhooks-succeeded-retention-days` / `webhooks.succeeded_retention_days` | 7 | Retention for `succeeded` delivery rows. `dead_letter` rows are never auto-deleted. |
| `--webhooks-poll-interval-seconds` / `webhooks.poll_interval_seconds` | 0 (auto) | Delivery-worker poll cadence. 0 ⇒ 2s on Postgres, 10s on SQLite. |
| `--webhooks-delivery-timeout-seconds` / `webhooks.delivery_timeout_seconds` | 10 | Per-POST timeout. |
| `--webhooks-max-attempts` / `webhooks.max_attempts` | 5 | Deliveries become `dead_letter` after this many failed attempts. |
| `--webhooks-backoff-base-seconds` / `webhooks.backoff_base_seconds` | 10 | Exponential backoff base. |
| `--webhooks-backoff-cap-seconds` / `webhooks.backoff_cap_seconds` | 600 | Backoff cap. |
| `--webhooks-shutdown-timeout-seconds` / `webhooks.shutdown_timeout_seconds` | 30 | Graceful drain of in-flight deliveries on SIGTERM. |

## Build

```bash
make build-all    # Server + CLI + UI
make build-ui     # UI only
make test         # Unit + integration tests
make test-e2e     # End-to-end suite (boots NIS + real NATS in Docker, asserts permissions)
```

`make test-e2e` is the regression net for refactors of the server, services, NATS plumbing, persistence, or encryption layers. CI runs it on every PR; run it locally after non-trivial server-side changes. Requires the Docker daemon for the NATS container; uses random TCP ports so it can run alongside `make run`. The suite is split into per-scenario files under `tests/e2e/` (`lifecycle`, `nats_live`, `export`, `import_backup`, `import_nsc`, `delete`, `observability`) with a shared harness in `harness_test.go`; each test boots its own NIS process (and NATS container, when needed) so a failure in one scenario doesn't cascade.

Convenience targets:

| Target | What it does |
|---|---|
| `make run` | Full dev stack: Postgres + NATS (Docker) + NIS (host) + admin user |
| `make run-demo` | `run` + JWT bootstrap (operator, demo cluster/account/user, creds file) |
| `make run-stop` / `run-clean` | Stop / wipe the dev stack |
| `make serve-local` | Legacy host-only server, SQLite, hardcoded dev secrets |
| `make docker-build` / `docker-run` / `docker-stop` | Single-container Docker image lifecycle |

## API tokens (service-account credentials)

For CI runners, deploy pipelines, and other non-interactive automation,
short-lived JWT sessions (issued by `AuthService.Login`) are awkward — they
expire and need refresh. NIS supports **long-lived opaque API tokens** that
ride the same `Authorization: Bearer <…>` header as JWTs and have their own
per-token role + scope.

Tokens have the prefix `nis_pat_` so they're easy to scan for in code or logs.
The plaintext is shown **once** at creation; only `sha256(token)` is stored.

```bash
# Mint a token for a CI runner.
nisctl token create --name ci-deploy --role operator-admin --operator demo-operator --expires-in 720h
#   Output ends with: nis_pat_abc123...      ← copy this; cannot be retrieved later

# Use the token in CI by exporting NIS_TOKEN, or with --token.
export NIS_TOKEN=nis_pat_abc123...
nisctl operator list
# or one-shot:
nisctl --token "$NIS_TOKEN" operator list

# List / revoke / delete.
nisctl token list
nisctl token revoke <id>
nisctl token delete <id>
```

The UI exposes a **"API Tokens"** page where any role can mint tokens within
their permissions and the plaintext is revealed in a one-time copy modal.

Behaviour and guard rails:

- **Privilege escalation guard.** A caller cannot mint a token with a role
  higher than their own, nor scoped outside their own operator/account. An
  operator-admin scoped to operator A cannot mint a token for operator B.
- **Chained-privilege block.** A token-authed call cannot use
  `CreateAPIToken` — i.e. a leaked token cannot bootstrap into a fresh
  long-lived credential and survive its parent's revocation.
- **Audit attribution.** Mutations driven by a token emit events with
  `actor_type='api_token'` and `actor_id=<token uuid>` — not the api_user
  that minted the token. The audit log identifies the credential actually
  in use.
- **`last_used_at`.** Updated by a coalescing background flusher (default
  every 30s) so high-rate CI traffic doesn't serialize writes against the
  authentication hot path. Configurable via
  `--api-tokens-last-used-flush-interval-seconds` /
  `API_TOKENS_LAST_USED_FLUSH_INTERVAL_SECONDS`.
- **`ExpiresAt` is optional.** A token with no expiry never expires; revoke
  it to disable.
- **Outliving the creator.** If the api_user who minted a token is deleted,
  the token stays valid (`ON DELETE SET NULL` on `created_by_user_id`). This
  is intentional — offboarding a human shouldn't silently break CI. Revoke
  the token explicitly if that's the goal.

CLI precedence: `--token` flag > `NIS_TOKEN` env > stored session from
`nisctl login`. Tokens are validated only on resource RPCs; they cannot be
used on `AuthService.Login` to mint a fresh JWT.

## API access

NIS exposes a Connect-RPC API (Protobuf over HTTP, both gRPC and gRPC-Web are
accepted on the same port as the UI). gRPC **server reflection** is enabled, so
clients can discover services and message schemas without a local `.proto` copy.
Reflection itself is unauthenticated by design — it returns schema only; the
underlying RPCs remain auth-gated.

The Dashboard has an "API Explorer" card with links and copy-pasteable
commands. Common entry points:

```bash
# List exposed services via reflection
grpcurl -plaintext localhost:8080 list

# Inspect a service
grpcurl -plaintext localhost:8080 describe nis.v1.OperatorService

# Call a method (token from /nis.v1.AuthService/Login, see auth.proto)
grpcurl -plaintext \
  -H "authorization: Bearer $TOKEN" \
  -d '{}' localhost:8080 nis.v1.OperatorService/ListOperators
```

Also works with **Postman**, **Bruno**, **Kreya**, and any other Connect/gRPC
client that supports reflection. Point them at your NIS URL.

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
| `nis_auth_rejections_total` | counter | `reason` | RPC rejected by the auth interceptor. `reason` ∈ `missing_token`, `invalid_token`, `forbidden`, `invalid_api_token`. |
| `nis_api_token_authentications_total` | counter | `status` | API-token auth outcomes. `status` ∈ `success`, `invalid`, `expired`, `revoked`. |
| `nis_events_emitted_total` | counter | `type` | Events appended to the audit log, by event type (e.g. `account.created`). |
| `nis_webhook_deliveries_total` | counter | `status` | Webhook deliveries by terminal status (`succeeded`/`failed`/`dead_letter`). |
| `nis_webhook_delivery_duration_seconds` | histogram | — | Per-attempt POST latency. |

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

Tracing options can be set via flag, env var (`TRACING_ENABLED`, `TRACING_ENDPOINT`, `TRACING_INSECURE`, `TRACING_SAMPLE_RATIO`, `TRACING_SERVICE_NAME`), or `config.yaml`:

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
