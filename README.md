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
# Builds NIS, starts Postgres + NATS + MinIO in Docker, runs NIS on the host
# pointing at all three, creates an admin user, writes .envrc with mc/aws-cli
# credentials for the local MinIO. UI at http://localhost:8080 (admin / admin123).

make run-demo
# Same as above, plus: creates a demo operator, restarts NATS with JWT auth on,
# registers a demo cluster/account/user, syncs JWTs, and writes a credentials
# file to .run/app-user.creds. Verify NATS works:
nats --creds=.run/app-user.creds --server=nats://localhost:4222 rtt
```

Lifecycle:

```bash
make run-status      # show what's running
make run-logs        # tail NIS server log (run-logs-nats / run-logs-pg / run-logs-minio for containers)
make run-stop        # stop server + remove containers (keeps Postgres + MinIO data volumes)
make run-clean       # full wipe (containers + volumes + ./.run/ + .envrc)
```

Local state (pid file, server log, generated NATS config, resolver/jetstream dirs, creds) lives in `./.run/` and is gitignored. Postgres data lives in the Docker volume `nis_dev_pg_data`; MinIO data in `nis_dev_minio_data`. Override defaults via env: `RUN_PG_PORT`, `RUN_PG_PASS`, `RUN_JWT_SECRET`, `RUN_ENC_KEY`, `RUN_MINIO_API_PORT`, `RUN_MINIO_USER`, `RUN_MINIO_PASS`, `RUN_MINIO_BUCKET` (see `Makefile`).

The NIS process started by `make run` has `BACKUPS_ENABLED=true` pointed at the local MinIO — scheduled backups work out of the box. A `.envrc` is written to the repo root (gitignored) with `MC_HOST_nis_dev=http://...@localhost:9000` and the matching `AWS_*` vars; source it (`source .envrc` or `direnv allow`) to use `mc ls nis_dev/nis-backups` or `aws --endpoint-url=$AWS_ENDPOINT_URL_S3 s3 ls s3://nis-backups` against the dev bucket. The alias is `nis_dev` (underscore), not `nis-dev` — `MC_HOST_<alias>` is read by `mc` from an env var, and env var names must be shell identifiers, which can't contain hyphens.

### Docker Compose (all-in-Docker, SQLite)

```bash
# Start all services (NIS + NATS, SQLite-backed)
docker-compose up -d

# Create the admin user (first run only)
docker exec nis-server ./nis user create admin --password admin123 --role admin

# Access UI at http://localhost:8080  (login: admin / admin123)

# Stop all services
docker-compose down
```

Once up, provision an operator, account, user, and credentials. The `admin` user
is a platform admin (org-less), so it must target an organization with `--org`
when creating an operator or referencing one by name. A default organization is
seeded on first start:

```bash
ORG=00000000-0000-0000-0000-000000000001   # seeded default org

./bin/nisctl operator create demo-operator --org $ORG
./bin/nisctl account create app-account --operator demo-operator --org $ORG
./bin/nisctl cluster create demo-cluster --operator demo-operator --org $ORG --urls nats://localhost:4222
./bin/nisctl cluster sync demo-cluster
./bin/nisctl user create app-user --operator demo-operator --account app-account --org $ORG
./bin/nisctl user creds app-user --operator demo-operator --account app-account --org $ORG > app-user.creds
nats --creds=app-user.creds --server=nats://localhost:4222 rtt
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

### Common issues

**Port already in use**:
```bash
lsof -ti:8080 | xargs kill -9
lsof -ti:4222 | xargs kill -9
```

**SQLite database locked** (Docker Compose):
```bash
docker-compose down
rm -f ./data/nis/nis.db-shm ./data/nis/nis.db-wal
docker-compose up -d
```

**NATS authentication fails after cluster registration**:
```bash
# Verify the account JWT was pushed
./bin/nisctl cluster sync demo-cluster
# Check NATS loaded the operator JWT
docker logs nis-nats | grep Operator
# Confirm credentials file is valid
head -5 app-user.creds
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

### Background-task intervals

Every recurring server task runs on the A2 jobs substrate (or a small set of
goroutines for tasks that pre-date it). All cadences are configurable in
seconds via `config.yaml`, env vars (`<UPPER_KEY>` with dots → underscores),
or — for the most commonly-tuned knobs — CLI flags. Defaults are sane for
prod; CI/dev typically wants the cluster-health and retention sweeps cranked
down.

| Key | Default | Flag | What it controls |
|---|---|---|---|
| `jobs.poll_interval_seconds` | 0 (auto: 2s PG / 10s SQLite) | `--jobs-poll-interval-seconds` | Job runner poll cadence. |
| `jobs.lease_duration_seconds` | 300 | `--jobs-lease-duration-seconds` | Per-claim lease; must exceed handler runtime. |
| `jobs.retention_sweep_interval_seconds` | 86400 | `--jobs-retention-sweep-seconds` | How often the `jobs.retention_sweep` handler runs. |
| `events.retention_sweep_interval_seconds` | 86400 | `--events-retention-sweep-seconds` | How often the `events.retention_sweep` handler runs. |
| `revocations.retention_sweep_interval_seconds` | 86400 | `--revocations-retention-sweep-seconds` | How often the `revocations.retention_sweep` handler runs. |
| `cluster.health_check_interval_seconds` | 60 | `--cluster-health-check-seconds` | Cluster-health goroutine cadence. |
| `cluster.health_check_initial_delay_seconds` | 5 | — | Initial delay before the first health check. |
| `metrics.domain_gauge_refresh_seconds` | 60 | `--domain-gauge-refresh-seconds` | In-memory inventory cache refresh cadence. |
| `jwt_policy.sweep_interval_seconds` | 3600 | — | `jwt.expiry_sweep` cadence. |
| `jwt_policy.expiry_lease_seconds` | 900 | — | Per-handler lease for `jwt.expiry_sweep`. |
| `backups.sweep_interval_seconds` | 3600 | — | `backup.sweep` cadence. |
| `backups.execute_lease_seconds` | 900 | — | Per-row lease for `backup.execute`. |
| `webhooks.delivery_timeout_seconds` | 10 | — | Per-POST HTTP timeout. |
| `webhooks.backoff_base_seconds` | 10 | — | Exponential-backoff base interval. |
| `webhooks.backoff_cap_seconds` | 600 | — | Exponential-backoff cap interval. |
| `api_tokens.last_used_flush_interval_seconds` | 30 | — | Coalesce window for API-token `last_used_at` updates. |

See `config.example.yaml` for the full reference with every recognised key.

### Runtime configuration inspection (admin-only)

The Admin UI at `/config` (visible under **Operations → Runtime Config** for
the `admin` role) renders the server's effective configuration as YAML after
the `flag > env > file > default` precedence chain has resolved. The same
data is available over the API as `ConfigService/GetRunningConfig`.

Credential values (`auth.jwt_secret`, `encryption.key`, `encryption.keys[].key`,
`backups.s3.access_key_id`, `backups.s3.secret_access_key`, plus any leaf
ending in `_secret`, `_password`, or `_access_key`) are replaced by
`***REDACTED***`. Key names stay intact so an admin can confirm a secret IS
configured without leaking its value. Env-only credentials (the documented
prod path for `AUTH_JWT_SECRET` etc.) are visible-and-redacted, not absent.

The `database.dsn` value is **smart-redacted**: only the password component
is replaced, so an admin still sees driver, host, port, user, dbname, and
sslmode. Both libpq key-value form (`host=... password=***REDACTED*** dbname=...`)
and URI form (`postgres://user:***REDACTED***@host/db`) are handled.

## Use Cases

**Multi-tenant SaaS** - Isolate customers with separate accounts
**Microservices** - Per-service credentials with scoped permissions
**Development** - Quickly provision test credentials
**Production** - Centralized credential management with encryption

## Backup & Restore

Operators (and everything underneath — accounts, users, scoped signing keys, clusters) can be backed up to and restored from a single file. Both **YAML** (default) and **JSON** are supported. Every backup carries seed material — there is no "metadata-only" mode, because a backup without seeds isn't restorable (the JWT's baked-in public key cannot be reproduced from a regenerated NKey pair).

```bash
# Default: seeds stay encrypted with the server's current encryption key.
# Restorable only by a server that uses the same key.
nisctl backup operator my-operator -o backup.yaml
nisctl backup operator my-operator --format json -o backup.json

# Disaster-recovery: decrypt seeds and emit them as plaintext NKey seeds.
# Restorable by ANY server (the restore re-encrypts with the destination's
# current key). Use this when you need a backup that survives encryption-key
# loss or rotation.
#
# DANGER: the resulting file is a plaintext NKey vault — protect it like a
# .creds file. Anyone with read access can mint credentials for every entity.
nisctl backup operator my-operator --plaintext-secrets -o backup-dr.yaml

# Restore. Format is auto-detected from the file contents — no flag needed,
# the same command handles either encoding, encrypted or plaintext.
nisctl restore backup.yaml
nisctl restore backup-dr.yaml

# Restore over an existing operator (same operator ID). Replaces the operator's
# subtree — accounts, users, scoped signing keys — atomically. Attached
# clusters are PRESERVED (they model live NATS infrastructure tied to the
# operator JWT, and restoring them from a stale backup would clobber
# running state). Without --overwrite, restoring over an existing operator
# ID is refused, since silently truncating accounts/users created since the
# backup is a footgun.
nisctl restore backup.yaml --overwrite
```

Restores are atomic: the whole flow runs in a single database transaction, so a mid-restore failure rolls every partial write back instead of leaving orphan accounts behind. The same applies to `nisctl import-nsc <archive> <operator-name>`, which ingests a tar/zip of an existing `~/.nsc/stores` tree (the other direction — migrating off `nsc`).

**Deleting an operator** is refused while clusters are still attached to it. `clusters.operator_id` is `ON DELETE RESTRICT` because clusters model live NATS servers configured with the operator's JWT — a silent cascade would lose track of running infrastructure. Delete (or detach by deleting) every attached cluster first, then the operator delete will succeed. The error message names the offending clusters so you know which to clean up.

## JWT lifecycle (P2)

NATS JWTs in NIS are **unbounded by default** — they carry no `exp`, matching the original behaviour. Per-operator policy can opt in to expiry, revocation, and auto-renew:

| Setting | Default | Meaning |
|---|---|---|
| `user_jwt_ttl_seconds` | `0` (never) | TTL stamped on every newly-minted user JWT. |
| `account_jwt_ttl_seconds` | `0` (never) | TTL stamped on the account JWT itself. Account JWTs live on the resolver; opt in only when you want signing-chain hygiene. |
| `jwt_warn_window_seconds` | `1209600` (14d) | Sweeper fires `user.cred.expiring_soon` when a user JWT's `exp` falls inside this window. Ignored when TTL is 0. |
| `jwt_auto_renew` | `false` | When `true`, sweeper re-signs an expiring-soon user JWT and emits `user.cred.renewed`. Holders still hold the old `.creds` — auto-renew makes a fresh JWT *available*, it does not redistribute. |

Configure per operator:

```bash
nisctl operator set-jwt-policy my-operator --user-ttl=2160h --warn-window=336h --auto-renew=false
# UI: Operators → <op> → "JWT Policy" card → Edit.
```

**Revocation.** A user revocation adds the user's NATS public key to the parent account JWT's NATS-native `Revocations` map and pushes the re-signed account JWT to every attached cluster. The user row is soft-revoked (`revoked_at` set); the row stays so audit + reinstatement work.

```bash
nisctl user revoke alice --operator my-op --account my-acc --reason "leaked laptop"
nisctl user regenerate-creds alice --operator my-op --account my-acc   # reinstates: clears revoked_at, mints fresh creds
```

The Account detail page exposes an **Active JWT revocations** panel listing the entries currently flattened into the account JWT's `Revocations` map. The "Still flagged" column shows whether the underlying user row's `revoked_at` is also set — it flips to **No** after `nisctl user regenerate-creds`, but the revocation row itself stays active in the account JWT until its `jwt_exp` (NATS keeps rejecting the old credential until then). This panel is the source of truth for "what NATS will actually reject right now."

**Already-expired credentials are NEVER auto-renewed.** If NIS was down through a TTL window and user JWTs expired, the sweeper emits `user.cred.expired` and waits — silently re-signing dead credentials defeats the point of expiry. An operator must run `nisctl user regenerate-creds` (or hit "Regenerate" in the UI) to issue a fresh JWT.

**Sweeper.** Runs on the A2 jobs substrate as the `jwt.expiry_sweep` handler, ticking every `jwt_policy.sweep_interval_seconds` (default `3600`). Phases per tick:

1. **Prune.** Revocations whose `jwt_exp` is past get marked `pruned_at` and the parent account JWT is regenerated without them, then pushed to all clusters. (NATS would reject the revoked JWT on `exp` anyway, so the entry would only bloat the account JWT.)
2. **Expiring-soon alert.** One `user.cred.expiring_soon` event per `iat` (dedup survives sweeper restarts).
3. **Auto-renew** (if `jwt_auto_renew=true` for the operator). Calls `RegenerateUserCredentials` internally.
4. **Expired alert.** `user.cred.expired` event for JWTs past `exp` — no auto-renew.

Force an immediate tick (admin-only):

```bash
nisctl operator run-jwt-sweep
# UI: Operators → <op> → "Admin Tools" card → "Run JWT Expiry Sweep Now".
```

**What expires what.** Regenerating a user JWT (manual or auto-renew) does NOT invalidate the previous JWT — the previously-issued `.creds` keeps working until its own `exp`. To forcibly invalidate the old creds, **revoke first**, then regenerate. Account permission changes (scoped-key edits, JetStream limits) re-sign and re-push the *account* JWT only — user JWTs already in the wild are unaffected; new permissions take effect on next reconnect because NATS reads scope templates off the account JWT, not the user JWT.

Tunable config keys:

```yaml
jwt_policy:
  sweep_interval_seconds: 3600
  sweep_batch_limit: 500
```

### Scoped signing key rotation (P3)

When a scoped signing key (SSK) is suspected compromised — leaked seed,
operator turnover, audit finding — rotate its NKey material in one shot.
Rotation:

1. Generates a new NKey pair for the SSK; the SSK row keeps its ID, name,
   permissions, template binding, and any drift flags. Only `public_key`
   and the encrypted seed change.
2. Re-mints every active dependent user's JWT under the new key.
3. Adds each dependent user's NATS public key to the parent account JWT's
   `Revocations` map with `revoked_at = now - 1s`, so the freshly-minted
   user JWTs (iat ≥ now) are accepted while the OLD JWTs are rejected.
   *(The 1-second backdate is mandatory because jwt v2's revocation check
   is `iat >= revoked_at` — without it, mints inside the same Unix second
   as the revocation moment would be born-revoked.)*
4. Re-signs the parent account JWT, pushing it to every attached cluster.

After rotation completes, **every existing `.creds` for users under the
rotated SSK stops working immediately**. Operators must distribute fresh
`.creds` (`nisctl user creds NAME ...`).

```bash
nisctl signing-key rotate KEY_ID --reason "leaked-in-repo-2026-05-22"
# UI: Signing keys → <key> → "Rotate key" card → confirm.
```

The RPC response (and CLI output) includes per-cluster push outcomes.
Clusters that lagged the push are named explicitly — the operator must
run `nisctl cluster sync <CLUSTER>` against each one to reconcile. The P9
sync drift dashboard also surfaces lag.

**Limitations in v1:**

- **Operator NKey** and **account main NKey** rotation are NOT supported.
  Operator pubkey is baked into every cluster's `nats-server.conf` via
  `operator <jwt>`; rotating it requires a coordinated cluster-config
  redeploy outside NIS. Account pubkey is a subject component
  (`$SYS.REQ.ACCOUNT.<accountPublicKey>.JSZ`) and the IssuerAccount on
  every user JWT under it — "rotating" it is operationally equivalent to
  creating a new account.
- **Plain-signer SSKs from NSC imports are refused** with
  `FailedPrecondition`. NIS doesn't own the permissions baked into those
  imported user JWTs; re-minting them with NATS-default perms would
  silently over-permission. Operator-facing path for these is to create
  a new NIS-native SSK and migrate users to it, then delete the imported
  SSK.

The rotation reason is captured as `ssk_rotation:<ssk_id>:<your text>` on
each `user_jwt_revocations` row, which the **Active JWT revocations**
panel surfaces — making rotation-induced revocations distinguishable
from individual operator-driven revokes.

## Events & Webhooks

Every mutation through NIS (create/update/delete on operators, accounts, users, scoped keys, clusters; cluster sync; cluster health transitions) is appended to a durable **events table** in the same transaction as the state change. Operators can subscribe HTTP endpoints to receive HMAC-signed POSTs when events fire. Used for audit trails, Slack/PagerDuty notifications, downstream cache invalidation.

### Event types

| Type | Emitted on |
|---|---|
| `operator.created` / `operator.updated` / `operator.deleted` | Operator lifecycle |
| `account.created` / `account.updated` / `account.deleted` | Account lifecycle (incl. JetStream limit changes) |
| `user.created` / `user.updated` / `user.deleted` | User lifecycle |
| `scoped_key.created` / `scoped_key.updated` / `scoped_key.deleted` | Signing-key lifecycle |
| `scoped_key.rotated` | Operator rotated an SSK's NKey material (P3). Payload carries old/new public keys, affected user count, and the list of revoked user public keys. Per-user `user.revoked` events are also emitted with `payload.triggered_by = "ssk_rotation"`. |
| `cluster.created` / `cluster.updated` / `cluster.deleted` | Cluster lifecycle |
| `cluster.synced` / `cluster.sync_failed` | Each `SyncCluster` call |
| `cluster.account.synced` | A single account's JWT was pushed to one cluster. Payload carries `trigger` ∈ `auto` (substrate-driven, A13-full auto-sync) or `manual` (operator-initiated `ReconcileAccountOnCluster` / P9 drift fix). |
| `cluster.account.deleted_from_resolver` | A successful `$SYS.REQ.CLAIMS.DELETE` for one account on one cluster (A13-full `cluster.account.delete` handler). |
| `cluster.health_changed` | The 60s probe sees a healthy→unhealthy or unhealthy→healthy transition |
| `webhook.test` | Operator clicks "Send Test" on a subscription |
| `webhook.subscription.created` / `webhook.subscription.updated` / `webhook.subscription.deleted` | Webhook subscription lifecycle (P1) |
| `api_token.created` / `api_token.revoked` / `api_token.deleted` | Service-account API token lifecycle |
| `api_user.created` / `api_user.password_changed` / `api_user.permissions_changed` / `api_user.deleted` | Local API-user account lifecycle (P1) |
| `user.revoked` | RevokeUser added a user's pubkey to the account JWT's revocations map |
| `user.cred.expiring_soon` | Sweeper found a user JWT expiring inside the operator's warn window |
| `user.cred.expired` | Sweeper found a user JWT past `exp` (auto-renew is NOT applied — explicit `RegenerateUserCredentials` required) |
| `user.cred.renewed` | RegenerateUserCredentials minted a fresh user JWT (manual or sweeper auto-renew) |
| `user.revocation_pruned` | Sweeper removed an expired revocation entry from an account JWT |
| `template.created` / `template.updated` / `template.deleted` | Permission template lifecycle (P6). `template.updated` carries `bumped:true` when the edit created a new version |
| `template.applied_to_scoped_key` | An SSK adopted a template version. `action` payload distinguishes `create` (SSK was created from the template), `bump` (existing SSK was rolled to a new version), and `detach` |

Events carry `actor_type` (`user` for RPC-driven events from human logins, `api_token` for RPCs driven by a service-account token, `system` for background ones), `actor_id` (the API user OR the token ID, depending on actor_type), `operator_id` / `account_id` scope, `resource_type` + `resource_id`, a free-form JSON `payload` with event-specific detail, and (on UPDATE events) a sparse field-level `diff_json` produced by P1.

The events table is **append-only**. Retention defaults to 30 days, configurable via `--events-retention-days` / `EVENTS_RETENTION_DAYS`. The audit log survives the deletion of the resources it references (operator_id is a soft scope, not an FK). FK CASCADE deletions inside the database (e.g. an operator delete that cascade-removes its $SYS account) emit only the top-level event — `operator.deleted` — not one event per cascaded row.

### Audit-log diffs (P1)

UPDATE-class events carry a `diff_json` field with the sparse field-level change set captured at the mutation site: `{"field_name": [before, after], ...}`. Only fields that actually changed are present; no-op updates leave the column NULL. CREATE/DELETE events do not carry a diff (the full state is already in the entity).

- **Sensitive fields are redacted automatically.** Field names with the suffixes `_secret`, `_password`, or `_access_key` (shared with the running-config redaction rules) are stored as `"***REDACTED***"` on both sides. JWTs, encrypted seeds, and password hashes are redacted via an explicit call at the emit site even though their names don't match the suffix.
- **Coverage is CI-enforced.** A lint test (`internal/interfaces/grpc/handlers/handler_emit_lint_test.go`) walks the authz registry and asserts every Create/Update/Delete mutation handler reaches an `events.EmitTx` / `events.EmitSystem` call in its service layer. New mutation RPCs fail the build until they emit. A small whitelist (≤5 entries with justification) carves out the legitimate non-emitters (e.g. job retry/cancel emit from the substrate, not the handler).
- **UI surface.** The Events page detail modal renders the diff as a two-column before→after table above the raw payload block.

### Browsing the log

- UI: **Events** page (admin-only). Filter by type, operator, exact since/until, actor (type + ID), resource ID, and free-text search across type/resource_id. Click a row for the full payload + diff.
- CLI: `nisctl event list [--type ...] [--operator ...] [--since 24h] [--limit 50] [--cursor ...]`, `nisctl event get <id>`.
- RPC: `EventService.ListEvents`, `EventService.GetEvent` (admin-only in v1; per-operator scoping is a v1.1 follow-up). `ListEventsRequest.filter` exposes `types`, `resource_type`, `resource_id`, `operator_id`, `account_id`, `actor_type`, `actor_id`, `search_q`, `since`, `until`, plus `limit`/`cursor`. `search_q` is a case-insensitive substring over `type` AND `resource_id`; payload is intentionally NOT searched.

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
| `--webhooks-delivery-timeout-seconds` / `webhooks.delivery_timeout_seconds` | 10 | Per-POST timeout. |
| `--webhooks-max-attempts` / `webhooks.max_attempts` | 5 | Deliveries become `dead_letter` after this many failed attempts. |
| `--webhooks-backoff-base-seconds` / `webhooks.backoff_base_seconds` | 10 | Exponential backoff base. |
| `--webhooks-backoff-cap-seconds` / `webhooks.backoff_cap_seconds` | 600 | Backoff cap. |

Webhook delivery runs on the generic jobs substrate (A2/A16). Poll cadence and shutdown drain are controlled by `jobs.poll_interval_seconds` and `jobs.shutdown_timeout_seconds`; the legacy `webhooks.poll_interval_seconds` / `webhooks.shutdown_timeout_seconds` keys are ignored. Per-delivery dispatch is visible in the JobsView UI under job type `webhook.deliver`.

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
| `make run` | Full dev stack: Postgres + NATS + MinIO (Docker) + NIS (host) + admin user + `.envrc` |
| `make run-demo` | `run` + JWT bootstrap (operator, demo cluster/account/user, creds file) |
| `make run-stop` / `run-clean` | Stop / wipe the dev stack |
| `make serve-local` | Legacy host-only server, SQLite, hardcoded dev secrets |
| `make docker-build` / `docker-run` / `docker-stop` | Single-container Docker image lifecycle |
| `make atlas-diff NAME=...` | Regenerate migrations from current GORM models (both dialects). See "Schema migrations" below. |
| `make atlas-lint` | Fail if models and migrations have drifted. Wire into CI. |
| `make migrate-up` / `migrate-down` / `migrate-status` | Apply migrations explicitly (`DRIVER=` and `DSN=` env vars). |

## Schema migrations (Atlas + Goose)

The GORM models in `internal/infrastructure/persistence/sql/models.go` are the source of truth for the schema. [Atlas](https://atlasgo.io) reads the models, computes the desired schema, and generates **per-dialect** Goose migration files into `migrations/sqlite/` and `migrations/postgres/` — separate trees because the two dialects need different SQL (sqlite inlines foreign keys with backtick identifiers; postgres uses `ALTER TABLE ADD CONSTRAINT` with double-quote identifiers). The NIS binary picks the right tree at runtime by driver.

```bash
# 1. Edit a model in internal/infrastructure/persistence/sql/models.go
# 2. Regenerate the migration for both dialects:
make atlas-diff NAME=add_widget_field

# Produces:
#   migrations/sqlite/<timestamp>_add_widget_field.sql
#   migrations/postgres/<timestamp>_add_widget_field.sql

# 3. Review the SQL, hand-edit if you need a data backfill or dialect quirk,
#    then update the checksum:
make atlas-hash

# 4. Rebuild + test
make build-server
make test && make test-e2e
```

Apply migrations manually (the server normally does this on startup when `--auto-migrate=true`):

```bash
make migrate-up   DRIVER=sqlite   DSN=./nis.db
make migrate-up   DRIVER=postgres DSN="host=localhost user=nis password=... dbname=nis sslmode=disable"
make migrate-status
make migrate-down DRIVER=...
```

Requirements: the [`atlas` CLI](https://atlasgo.io/getting-started#installation) and Docker (Atlas spins up a throwaway Postgres container as its diff target). The schema generator (`tools/atlas/loader.go`) is a regular Go program that runs under `go run` — no separate install.

Full workflow + the GORM tag conventions (FK declarations via relation fields, `default:` tag footgun, etc.) are in `.claude/skills/nis-dev/SKILL.md` §7.

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

## Organizations & SSO

NIS is multi-tenant. An **organization** is the top-level tenant: every
operator (and transitively every account, user, and cluster) belongs to
exactly one organization. Platform admins (`role=admin`) are org-less and see
everything; an **org-admin** manages exactly one organization and everything
beneath it.

Every existing deployment is migrated into a single **default organization**
(`00000000-0000-0000-0000-000000000001`); pre-existing operators and non-admin
api_users are assigned to it automatically. The default org cannot be deleted.

**Operator names are unique per organization, not globally.** Two different
organizations may each have an operator named `prod` without conflict; the
uniqueness constraint is on `(organization_id, name)`. Name-based lookups by a
platform admin that aren't org-scoped (e.g. `nisctl operator get <name>`) return
the single match if there's exactly one, and fail asking you to disambiguate by
org if the same name exists in more than one organization.

```bash
# Org CRUD (create/delete is admin-only; org-admins can read+update their own).
nisctl org create "Acme Corp" --slug acme --description "Acme tenant"
nisctl org list
nisctl org get <id>            # or: nisctl org get --slug acme
nisctl org update <id> --name "Acme Inc"   # slug is immutable
nisctl org delete <id>

# Assign a new operator to an org. Platform admins MUST pass --org (use the
# default org UUID 00000000-0000-0000-0000-000000000001 for the default org);
# org-scoped tokens omit it and land in their own org.
nisctl operator create acme-operator --org <org-id>
```

### OIDC SSO configuration

Each organization can have one OIDC SSO configuration (e.g. Authentik, Keycloak,
Dex, Okta). The client secret is **write-only** — it is encrypted at rest and
never returned by any read RPC (`org sso get` shows `client_secret_set: true`).

```bash
nisctl org sso set <org-id> \
  --issuer https://idp.example.com/application/o/nis/ \
  --client-id nis \
  --client-secret 's3cret' \
  --scopes "openid profile email groups" \
  --group-claim groups \
  --default-role account-admin \
  --enabled
nisctl org sso get <org-id>
nisctl org sso delete <org-id>
```

**Group → role mappings** map an IdP group value to a NIS role + scope. They are
replace-all (the whole set is set atomically). A mapping's role may be
`org-admin`, `operator-admin`, or `account-admin` — **never `admin`** (SSO can
never grant platform-admin; this is enforced server-side). `--default-role`
likewise cannot be `admin`, and an empty default role denies login when no
mapping matches.

```bash
# Repeatable --mapping spec: group_value:role:priority[@scope_id]
nisctl org sso mappings set <org-id> \
  --mapping "nis-admins:org-admin:10" \
  --mapping "platform:operator-admin:20@<operator-uuid>"

# Or a JSON array via --file (use "-" for stdin):
nisctl org sso mappings set <org-id> --file mappings.json
nisctl org sso mappings list <org-id>
```

Mappings are evaluated by ascending priority (lowest first); first match wins.

### Logging in via SSO (Web UI)

Once an org has SSO enabled, users sign in from the NIS login page:

- The login page has a **"Continue with SSO"** field — enter the org **slug** and
  it navigates to `/auth/oidc/start?org=<slug>`, which 302-redirects to the IdP.
- Deep link: `/login?org=<slug>` auto-triggers the redirect (handy for bookmarks
  and IdP-initiated "launch" tiles).
- After the IdP authenticates the user, NIS handles `/auth/oidc/callback`,
  JIT-provisions or finds the matching `api_user` (role resolved from the group
  mappings), and redirects to the SPA `/login/callback#token=<jwt>`. The SPA
  reads the session JWT from the URL fragment and immediately strips it from the
  address bar (`history.replaceState`) so it never lands in history or `Referer`.
- The username/password form remains a **break-glass** path for the platform
  admin and any local users, and is unaffected by SSO being enabled or an IdP
  being unreachable.

SSO-provisioned users are **OIDC-managed**: their role comes from the group
mappings on every login, so manual password/role edits on those rows are
rejected (change the mapping instead).

> **`server.public_url` is required for SSO.** It is the externally-reachable
> base URL NIS uses to build the OIDC `redirect_uri`
> (`<public_url>/auth/oidc/callback`) and the post-login SPA redirect. Set it in
> `config.yaml` (`server.public_url`) or via the matching env var/flag. With it
> empty, the OIDC start endpoint cannot construct a valid callback and login
> fails. It must exactly match a redirect URI registered in the IdP client. The
> SSO Configuration card in the org detail view displays this exact redirect URI
> (with a copy button) so you can paste it into your IdP; if `server.public_url`
> is unset it shows a warning instead of a URL.

Platform admins manage organizations from the **Organizations** nav item;
org-admins get a **My Organization** item that opens their org's detail page
(SSO config editor, role-mapping editor, and org-scoped user management).

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

## Pagination (A7)

Every List* RPC in NIS uses **keyset cursor pagination** with SQL-level
tenant scope enforcement. There is no post-fetch filter: scope narrowing
happens entirely inside the database query.

### Covered RPCs

| Service | RPC | Filters |
|---|---|---|
| OperatorService | ListOperators | `name_like` |
| AccountService | ListAccounts | `operator_id`, `name_like` |
| UserService | ListUsers | `account_id`, `name_like` |
| ScopedSigningKeyService | ListScopedSigningKeys | `account_id`, `name_like` |
| ClusterService | ListClusters | `operator_id`, `name_like` |
| TemplateService | ListTemplates | `operator_id`, `name_like` |
| WebhookService | ListWebhookSubscriptions | `operator_id`, `enabled`, `event_type_match` |
| WebhookService | ListWebhookDeliveries | `subscription_id`, `status` |
| APITokenService | ListAPITokens | `include_revoked` (admin: `created_by_user_id`) |
| AuthService | ListAPIUsers (admin: all; org-admin: own org) | `role`, `username_like`, `auth_source` |
| BackupService | ListOperatorBackups | `operator_id`, `trigger_kind` |
| JobService | ListJobs (already cursor-paginated pre-A7) | types, statuses, since/until |
| EventService | ListEvents (already cursor-paginated pre-A7) | types, resource, since/until |

### How it works

- Results are ordered `created_at DESC, id DESC` (deliveries use
  `scheduled_at DESC, id DESC`). The cursor encodes the
  `(created_at, id)` of the last row seen.
- Passing a cursor returns the page strictly after that point. An empty
  `next_cursor` in the response means no more pages.
- Limit defaults: most resources clamp to 200 max with a 50 default.
  Events keeps its shipped 100 default / 1000 max.

### CLI

By default the list commands fetch **all pages** in a loop and print the
full result set — `nisctl <noun> list | wc -l` and similar pipes work as
expected:

```bash
nisctl operator list
nisctl account list my-operator
nisctl user list app-account --operator my-operator
nisctl signing-key list app-account --operator my-operator
nisctl cluster list
nisctl template list --operator my-operator
nisctl webhook list
nisctl webhook deliveries SUBSCRIPTION_ID
nisctl token list
```

Single-page mode is activated by setting `--limit` or `--cursor`
explicitly:

```bash
# First page, 10 results, show next cursor on stderr
nisctl operator list --limit 10

# Next page using printed cursor
nisctl operator list --limit 10 --cursor <cursor>

# Filter by name
nisctl operator list --name-like prod
```

### UI

The Vue UI uses **prev/next page navigation** with a client-side cursor
stack — never "Load more" / infinite scroll. Filter changes reset the
cursor stack to page 1.

### Scope enforcement

Tenant scope lives in the `internal/application/authz.Scope` type and is
passed as a required parameter into every `ListPage` repository method.
Omitting it is a compile error.

| Role | Sees |
|---|---|
| Admin / System | All records |
| Operator-admin | Only records within their operator |
| Account-admin | Only records within their account; operators/clusters/templates of the parent operator |

`APIToken` adds per-caller self-scope on top: non-admin callers see only
tokens they created. `APIUser` is admin-only.

## Global search (P11)

The web UI has a top-bar global search and `nisctl search QUERY` exposes the
same surface on the CLI. Both call `nis.v1.SearchService/Search` and return
matches across **operators, accounts, users, scoped signing keys, and clusters**
in one round-trip. The intent is to answer "which scoped key allows pub on
`metrics.>`?" with a single query instead of clicking through every account.

What is searched:

- Operator / Account / User: name, description, public key.
- Cluster: name, description, server URLs.
- Scoped Signing Key: name, description, public key, AND the pub-allow /
  pub-deny / sub-allow / sub-deny subject lists. Subject queries match both the
  raw form and the JSON-encoded form (so `metrics.>` finds keys regardless of
  how `>` was escaped at storage time).

Behavior:

- Case-insensitive on both SQLite and Postgres (`LOWER(col) LIKE LOWER(?)`).
- Query is trimmed and must be 2..128 characters; SQL `LIKE` metacharacters
  (`%`, `_`) in user input are escaped to literals on the user-text columns.
- Results are narrowed to the caller's RBAC scope by `PermissionService`
  *before* being returned — operator-admins only see their own operator's
  subtree even when the LIKE query matches another operator's row. The
  filtering uses the same `Filter*` helpers as every List RPC.
- Limit applies per kind (default 20, cap 100).
- Each result row is labelled in the UI with the **owning operator's name**,
  so name collisions across operators (`$SYS`, `system`, `default`) are no
  longer ambiguous. The response carries two side-band lookup maps
  (`operator_names`, `account_operators`) that the UI chains for Users and
  ScopedSigningKeys (which only reference their account natively).

Deliberately excluded from the search surface: API users, API tokens, webhook
subscriptions, and events. These are credentials / audit surfaces with their
own access patterns and shouldn't be exposed by free-text search.

```bash
# Find any entity matching "metrics"
nisctl search metrics

# Narrow to scoped signing keys, look up which keys touch a subject
nisctl search 'metrics.>' --kind scoped-key

# Limit to operators and accounts
nisctl search payments --kind operator,account --limit 10
```

## Live JetStream usage (P10)

JetStream limits configured on an account (`max_memory`, `max_storage`, `max_streams`,
`max_consumers`) tell NATS what an account is *allowed* to use. They say nothing about
what it's *actually* using. NIS now queries each attached cluster live via
`$SYS.REQ.ACCOUNT.<accountPublicKey>.JSZ` using the cluster's stored system-account
credentials and returns per-cluster usage you can put next to the limits.

```bash
# Show per-cluster usage for an account
nisctl account jetstream-usage app-account --operator demo-operator

# Force a dial against clusters marked unhealthy (slower; pays the dial timeout)
nisctl account jetstream-usage app-account --operator demo-operator --include-unhealthy
```

The UI surfaces the same data on the Account detail page as four progress bars
(memory, storage, streams, consumers) per cluster, with a manual Refresh button —
no auto-poll, to avoid ambient NATS load.

Per-cluster status codes:

- `ok` — query succeeded; usage populated.
- `unreachable` — dial failed, request timed out, or the cluster was marked
  unhealthy by the 60s health-check loop AND a check actually ran (clusters that
  haven't been checked yet are still dialed; never-checked is not treated as
  failed).
- `no-jetstream` — cluster responded but JS is not enabled for this account on
  this cluster.
- `not-activated` — the account's JWT has JS enabled, but NATS hasn't
  initialised per-account JS state yet. NATS does this lazily on the first
  client connect (or first JS API call) from the account. Connect once
  (`nats --creds=app.creds rtt`) or publish to a JS subject and refresh.
  Usage bars render as 0 / max — accurate, nothing has been used yet.
- `account-not-found` — cluster has no record of the account (no JWT pushed, or
  sync drift). Distinct from `not-activated`: the JWT itself is missing.
- `error` — any other failure (decryption, malformed response, etc.).

What is reported per cluster (counts and bytes are cluster-wide aggregates):

- `memory_used` / `storage_used` — bytes currently in use vs. the account JWT's
  `max_memory` / `max_storage` limit.
- `reserved_memory` / `reserved_storage` — bytes reserved by streams.
- `streams` / `consumers` — count across all streams in the account.
- `api_total` / `api_errors` — cumulative JetStream API call counts for the
  account (useful as a "did this account talk JS at all" tripwire).

This is a read-only inspection — no DB writes, no events, no cluster mutation.
The system user already has access to `$SYS.REQ.ACCOUNT.*` by design.

## Sync drift detection (P9)

`nisctl cluster sync` pushes the NIS-DB JWTs to a cluster's NATS full-resolver,
but once that completes there's no built-in way to know whether the resolver
still agrees with NIS: a forgotten regen, a partial sync, or someone running
`nsc push` out-of-band can leave the two stores diverged. The drift dashboard
compares NIS's stored JWT against the resolver's JWT for every account on the
operator and reports the relationship per row.

```bash
# Show drift for one cluster (in-sync accounts hidden by default)
nisctl cluster drift demo-cluster

# Include in-sync rows too — useful as a full inventory
nisctl cluster drift demo-cluster --include-in-sync

# Push one account's JWT to one cluster (the "fix this row" action)
nisctl cluster reconcile-account demo-cluster --operator demo-operator --account app-account
```

The UI surfaces the same data on the Cluster detail page: an "Account sync
status" table with a Refresh button (manual; no auto-poll, to avoid ambient
NATS load) and a per-row Reconcile button. Reconcile pushes the single
account's NIS-stored JWT to the cluster — much cheaper than a full
`SyncCluster` when only one row is out of date.

Per-account status values:

- `in_sync` — resolver returned a JWT that matches NIS's stored JWT.
- `db_ahead` — both decode; NIS's JWT was issued later than the resolver's.
  Standard "regenerated, didn't push yet" state. Reconcile pushes the new JWT.
- `out_of_band` — resolver has a JWT NIS does not recognise (resolver's iat is
  newer than NIS's, or contents diverge at equal iat). Something other than
  NIS pushed to this resolver — investigate. Reconcile from NIS overwrites
  the resolver.
- `missing_on_resolver` — NIS has the account in its DB but the resolver
  returns no JWT for the account's public key. Reconcile pushes the JWT.
- `unreachable` — cluster could not be probed (dial failed, no resolver
  responder, decrypt failure, timeout). Per-row `error_message` carries the
  underlying reason. No drift assertion is possible until the cluster is
  reachable again.
- `orphan_on_resolver` — the resolver has a JWT for an account NIS has no
  record of. Caused by a legacy direct push, a `DeleteAccount` whose NATS-side
  cleanup failed mid-flight (DB row gone, resolver still has the JWT), or a
  manually-managed tenancy. Shown explicitly because leaked `.creds` minted
  under an orphan JWT keep connecting until something prunes them. The UI
  surfaces a per-row "Delete from resolver" action that calls
  `DeleteResolverAccount`; bulk cleanup is still `nisctl cluster sync --prune`.

The comparison primitive is cheap: equality on the raw encoded JWT first
(the common IN_SYNC case after a fresh sync), and only on mismatch does it
decode both via `nats-io/jwt/v2` and classify by issued-at ordering. The
event `cluster.account.synced` fires on a successful reconcile so the audit
log records who pushed what to where.

Account deletion auto-propagates to the resolver: `DeleteAccount` sends an
operator-signed `$SYS.REQ.CLAIMS.DELETE` to every cluster attached to the
operator after the DB transaction commits. Per-cluster delete failures are
logged but do NOT roll back the DB delete (DB is the source of truth, NATS
reconciled best-effort) — if a delete happens while a cluster is unreachable,
the JWT survives on that cluster's resolver and is surfaced as
`orphan_on_resolver` on the next drift scan, ready for one-click cleanup.

Drift detection and reconcile are scoped to admin (same gate as `SyncCluster`).

## Bulk operations (manifest apply/diff/delete/dump)

NIS supports a declarative, kubectl-style workflow for managing the full
identity tree. You describe the desired state in YAML, and `nisctl apply`
creates or updates only the entities that differ from what is already on the
server. A multi-document YAML file (documents separated by `---`) can declare
all five kinds in a single file and apply them in the correct topological order
(Operator → Cluster → Account → ScopedSigningKey → User).

### Quick example

```yaml
---
apiVersion: nis/v1
kind: Operator
metadata:
  name: acme-prod
spec:
  description: "ACME production operator"
---
apiVersion: nis/v1
kind: Account
metadata:
  name: payments
  operator: acme-prod
spec:
  description: "Payment processing account"
---
apiVersion: nis/v1
kind: User
metadata:
  name: payments-svc
  operator: acme-prod
  account: payments
spec:
  description: "Service account for the payments microservice"
  jwtTTL: 168h
```

### Commands

```bash
# Compute what would change and print the plan — no server writes
nisctl diff -f manifest.yaml
# or equivalently:
nisctl apply -f manifest.yaml --dry-run

# Apply the plan; prompts for confirmation unless -y is given
nisctl apply -f manifest.yaml
nisctl apply -f manifest.yaml -y

# Delete every entity declared in the file (reverse topo order: User first, Operator last)
nisctl delete -f manifest.yaml
nisctl delete -f manifest.yaml -y

# Dump an existing operator tree to stdout as a manifest
nisctl dump operator OPERATOR_NAME
nisctl dump operator OPERATOR_NAME -o acme-prod.yaml
# Restrict to specific kinds (comma-separated):
nisctl dump operator OPERATOR_NAME --kinds=Account,User
```

### Organization targeting (`--org`)

`--org <org-uuid>` is a **single global flag** on `nisctl` (a persistent root
flag), not a per-command one. It governs every command that creates an operator
or references one by name — `operator create`, `operator get/delete/backup/dump/
generate-include`, `account create --operator …`, `api-user create`,
`token create`, and `apply`/`diff`/`delete -f`. Manifests themselves never carry
an `organization_id`; the target org is resolved server-side from your
credential plus `--org`, so the same file applies cleanly into any org.

Operator names are unique **per org**, not globally — so a name alone is
ambiguous and the server never guesses:

- **Org-scoped credentials** (an org-admin login, or an API token minted with a
  non-admin role) are pinned to their own organization. Operators they create or
  look up by name resolve within that org automatically; you do **not** pass
  `--org`, and it is ignored if you do — a token cannot reach into another org.
- **Platform admins** (`role=admin`) have no org binding, so they **must** pass
  `--org` for any operator create OR by-name lookup:

  ```bash
  nisctl operator create acme-op   --org <org-uuid>
  nisctl operator get    acme-op   --org <org-uuid>
  nisctl apply  -f manifest.yaml   --org <org-uuid>
  nisctl diff   -f manifest.yaml   --org <org-uuid>
  nisctl delete -f manifest.yaml   --org <org-uuid>
  ```

  Omitting `--org` as a platform admin fails with
  `organization_id is required: platform admins must specify the target
  organization`. (For the default org, pass
  `--org 00000000-0000-0000-0000-000000000001`.) `--org` takes a UUID; the org
  slug is not accepted here in v1.

Because operator names are unique per org (not globally), the same manifest can
be applied into several organizations to stamp out identical operator trees.

### Reserved names

The following entities cannot be declared or deleted via manifest:

- **`$SYS` Account** — auto-created with every operator; not manageable via manifest.
- **`system` User** — auto-created under `$SYS`; not manageable via manifest.
- **`default` ScopedSigningKey** — auto-created per account; may be declared in a
  manifest to update its permissions, but cannot be deleted via manifest.

### Bulk-manifest workflow tip

```bash
# Capture current state
nisctl dump operator acme-prod > acme-prod.yaml
# Edit the file, then preview the delta
nisctl diff -f acme-prod.yaml
# Apply when happy
nisctl apply -f acme-prod.yaml
```

### v1 limitations

- **Cluster updates** are not supported via manifest; the `apply` command will
  error on an existing Cluster that differs from the server state. Use
  `nisctl cluster update` or the UI for in-place cluster edits.
- **User `scopedKey` reassignment** via apply is not supported. Changing
  `spec.scopedKey` on an existing user would invalidate the user's current
  `.creds` file. Delete and recreate the user explicitly instead.

Full per-kind examples with annotated fields: [`example/manifests/`](example/manifests/).

## Permission templates (P6)

Templates are operator-scoped, versioned permission bundles. Instead of
re-typing the same `pub_allow`/`sub_deny` lists across 50 scoped signing
keys for a "ServiceReader" role, declare it once as a `Template`, then
create SSKs from it with `--from-template`. The SSK carries a snapshot
of the template's permissions plus a pin to a specific version.

**Template updates never auto-cascade.** Bumping a template creates a
new `template_versions` row and advances `latest_version`; SSKs pinned
to an older version stay on it until an operator explicitly bumps each
one. This is deliberate — surprise permission rollouts to production
clusters are exactly what P6 exists to prevent.

```bash
# Create a template (this stamps v1).
nisctl template create service-reader --operator my-op \
  --description "Read-only service consumer" \
  --sub-allow "events.>" --sub-allow "_INBOX.>" \
  --pub-deny ">"

# Create a standalone SSK with explicit permissions (no template).
nisctl signing-key create reader-ssk --operator my-op --account web \
  --description "service reader role" \
  --pub-allow "svc.>" --pub-deny "svc.admin.>" \
  --sub-allow "svc.>" --sub-allow "_INBOX.>" \
  --response-max-msgs 1 --response-ttl 2m

# Or create an SSK from the template. The SSK is pinned to the template's
# current latest_version unless --template-version is set. The perm
# flags above are refused when --from-template is set — the template
# wins.
nisctl signing-key create reader-ssk --operator my-op --account web \
  --from-template service-reader

# Edit an SSK in place. Metadata flags (--name, --description) and
# permission flags can be combined; only the flags you pass change.
# Permission edits re-sign the parent account JWT and push to clusters.
nisctl signing-key edit <SSK_ID> --description "narrowed scope"
nisctl signing-key edit <SSK_ID> --pub-deny "svc.admin.>,svc.internal.>"

# Update the template (new permissions ⇒ new version row + latest_version bump).
# Description-only updates do NOT bump the version. `edit` is an alias.
nisctl template update service-reader --operator my-op \
  --sub-allow "events.>" --sub-allow "metrics.>" --sub-allow "_INBOX.>" \
  --pub-deny ">" \
  --change-note "add metrics read access"

# Existing SSKs still on v1 until explicit roll-out. List + bump:
nisctl template dependents service-reader --operator my-op
nisctl signing-key bump-template <SSK_ID>          # to current latest
nisctl signing-key bump-template <SSK_ID> --to-version 2

# Detach an SSK from its template — keeps current permissions, stops
# tracking. After detach, future template updates have no effect.
nisctl signing-key detach-template <SSK_ID>
```

**Drift flag.** Editing a templated SSK's permissions directly (via
`UpdatePermissions` / the UI) sets `template_drifted=true`. The
template binding is preserved; the UI surfaces an "edited" badge so
operators can decide whether a future bump should overwrite their
custom edits or whether to detach first.

**Reserved names.** `default` and `system` are refused as template
names (collisions with the per-account default SSK and the `$SYS`
system user).

**Manifests.** `Template` is a new kind; SSK specs gain optional
`template` + `templateVersion` fields. See
[`example/manifests/template.yaml`](example/manifests/template.yaml)
and [`example/manifests/full-stack.yaml`](example/manifests/full-stack.yaml).

**Auto-sync (A13-full, 2026-05-22).** Account, SSK, template, user-revoke,
and revocation-prune mutations now enqueue one `cluster.account.push` job
per attached cluster INSIDE the same DB transaction. The substrate
(`jobs.poll_interval_seconds`, default 2s PG / 10s SQLite) picks up the
row and pushes the regenerated account JWT to NATS with retries and
backoff. Account deletes enqueue `cluster.account.delete` jobs that send
`$SYS.REQ.CLAIMS.DELETE` to each cluster's resolver. Per-cluster
failures live on the job row's `last_error` and surface via JobsView +
the [sync drift dashboard](#sync-drift-detection-p9); `nisctl cluster
sync` remains the manual recovery tool.

Stay-synchronous carve-outs (NOT routed through the substrate):
`RotateScopedSigningKey` (returns per-cluster outcomes inline in the RPC
response — operators rely on it), `SyncCluster` (manual via `nisctl
cluster sync`, user is waiting), and `ReconcileAccountOnCluster` (P9
manual reconcile). The `cluster.account.synced` event payload now carries
a `trigger` field ("auto" for substrate-driven, "manual" for operator-
initiated) so webhook subscribers can distinguish them.

Replaces A13-lite (the in-process post-commit push that shipped with P6).
A13-lite lost pushes on NIS-crash-between-commit-and-push windows; A13-
full's in-tx enqueue closes that window.

## Background jobs (A2)

NIS runs scheduled and one-shot work on a single durable jobs substrate
instead of a pile of in-process goroutines. The shipped v1 handlers are
the two retention sweeps:

| Handler | Cadence | What it does |
|---|---|---|
| `events.retention_sweep` | 24h | Deletes events older than `events.retention_days` (default 30d) and succeeded webhook deliveries older than `webhooks.succeeded_retention_days` (default 7d). Dead-letter deliveries are never auto-deleted. |
| `jobs.retention_sweep`   | 24h | Deletes `succeeded` and `cancelled` job rows older than `jobs.retention_days` (default 30d). `failed` and `dead_lettered` rows survive forever — operator audit. |
| `revocations.retention_sweep` | 24h | Hard-deletes `user_jwt_revocations` rows whose `pruned_at` is older than `revocations.retention_days` (default 90d). Active revocations (`pruned_at IS NULL`) are NEVER touched — they still belong in the parent account JWT's NATS `Revocations` map. Set `revocations.retention_days=0` to disable. Batch-capped at 500 rows per tick. |
| `webhook.deliver`        | per-delivery, in-tx enqueue | Dispatches one webhook subscription POST per row. Inserted into the same tx as the `webhook_deliveries` row (atomic). Permanent failures (subscription disabled, decrypt error, malformed payload) signal `ErrPermanentJobFailure` and dead-letter the job immediately; transient (5xx, transport) retry with the substrate's backoff up to `webhooks.max_attempts`. `AuditNone` — per-delivery audit lives on the typed `webhook_deliveries` row. |
| `backup.sweep` / `backup.execute` | sweep 1h, execute per due operator | Per-operator scheduled backups to S3. See "Scheduled backups (P12)" below. |
| `jwt.expiry_sweep`       | `jwt_policy.sweep_interval_seconds` (default 1h) | Drives the four-phase JWT lifecycle sweeper (P2): prune past-exp revocations → expiring-soon alert → optional auto-renew → expired alert. `LeaseDuration` is 15m at the handler level (the auto-renew phase can re-sign and push N cluster JWTs per operator). `AuditFailuresOnly` — the sweeper emits its own per-user `user.cred.*` semantic events, so substrate audit would triple-emit. |
| `cluster.health.sweep` / `cluster.health_check` | sweep `cluster.health_check_interval_seconds` (default 60s) | A15. Per-cluster health probe. Sweep enumerates clusters and EnsureScheduled-s one check per row; each check is one-shot (`MaxAttempts=1`) because failure state lives on the cluster row, not the job. |
| `cluster.account.push` / `cluster.account.delete` | per-mutation, in-tx enqueue | A13-full. Pushes a single account JWT to one cluster (or deletes via `$SYS.REQ.CLAIMS.DELETE`). One row per `(account, cluster)`; the partial unique index collapses bursts. After the push, the handler re-reads `account.JWT`; if it changed during the push, a follow-up keyed on the new JWT hash is enqueued so the dedup index can't suppress staleness fixes. `MaxAttempts=3`, `AuditFailuresOnly` (the handler emits `cluster.account.synced` on success; substrate audit would duplicate). |

Future scheduled work — see [DESIGN.md](DESIGN.md) for the
follow-up roadmap.

### Admin surface

The substrate is **admin-only**. operator-admin and account-admin get
`PermissionDenied` on every JobService method.

**UI:** *Background Jobs* under the user menu (admin only).
Filter by type / status / since, default view "non-succeeded in last 24h",
explicit *Show succeeded* toggle for auditing successful runs after the
fact. Per-row Retry (for `failed`/`dead_lettered`/`cancelled`) and Cancel
(for `pending` rows). JSON detail modal.

**CLI:**

```bash
nisctl job list [--type ...] [--status ...] [--since 24h] [--limit N]
nisctl job get   <id>
nisctl job retry <id>     # resets attempts to 0, status to pending
nisctl job cancel <id>    # only succeeds on pending rows
```

### Semantics worth knowing

- **No dedup_key = no dedup.** SQL `UNIQUE` treats NULLs as distinct, so
  rows without a dedup key can pile up — that's deliberate (one-shot
  jobs). The partial unique index
  `(type, dedup_key) WHERE status IN ('pending','running')` only
  catches rows where the operator opted in via `WithDedupKey`.
- **Recurring schedules use a per-tick watchdog.** Handlers register
  with `HandlerSpec.RecurEvery > 0`; on every poll tick the runner
  calls `EnsureScheduled` to insert a future row if one doesn't already
  exist. That survives both handler crashes and process restarts —
  there's no per-handler bootstrap code to forget.
- **Retry resets attempts to 0.** Admin retry is "give this another
  chance," not "continue from where the lease left off." If you want
  the row to keep its attempt count, don't retry — let the runner's
  built-in backoff path handle it.
- **Cancel only works on pending rows.** A running row can't be
  cancelled safely (the handler is mid-flight; cancelling the row
  doesn't kill the goroutine). Wait for it to finish or for the lease
  to expire, then retry/cancel the resulting state.

### Tunables (server-side)

| Config key | Default | Notes |
|---|---|---|
| `jobs.poll_interval_seconds` | `0` (auto) | 2s on Postgres, 10s on SQLite when 0. Override only if you need faster ticks for testing. |
| `jobs.claim_batch` | `10` | Rows claimed per tick. |
| `jobs.lease_duration_seconds` | `300` | Must exceed any handler's expected runtime; a row whose lease expires gets reclaimed by the next worker. |
| `jobs.shutdown_timeout_seconds` | `30` | Graceful in-flight drain on SIGTERM. |
| `jobs.retention_days` | `30` | For `jobs.retention_sweep` — drops succeeded/cancelled rows older than this. |
| `revocations.retention_days` | `90` | For `revocations.retention_sweep` — hard-deletes `user_jwt_revocations` rows soft-pruned more than this many days ago. Set to `0` to disable. Active (non-pruned) rows are never touched. |
| `revocations.retention_sweep_interval_seconds` | `86400` | How often `revocations.retention_sweep` runs. Also tunable via `--revocations-retention-sweep-seconds`. |

## Scheduled backups (P12 + P15)

NIS can upload per-operator backups to any S3-compatible object store on a
configurable cadence. The bytes are produced by the same export path that
backs `nisctl backup operator` and are encrypted with [age](https://age-encryption.org/)
to the operator's recipient list before upload (P15, 2026-05-25). Bucket
leak ≠ identity-tree leak: an attacker with the S3 object plus no age
identity gets opaque ciphertext.

**Three layers of opt-in.** Backups are disabled by default at the
NIS-wide level (`backups.enabled=false`). When enabled, individual operators
still need an explicit `nisctl operator backup enable <name>` before the
sweep starts producing artifacts for them, AND must register at least one
age recipient public key — a scheduled run with zero recipients fails
loudly (`operator.backup.failed` reason="no_recipients_configured"). Flip
any of the three off to stop scheduled backups for that scope.

### NIS-wide configuration

| Config key | Default | Notes |
|---|---|---|
| `backups.enabled` | `false` | Master switch. When false, BackupService RPCs return `FailedPrecondition`. |
| `backups.sweep_interval_seconds` | `3600` | How often the sweep enumerates due operators. Each operator's `interval_seconds` is independent. |
| `backups.s3.endpoint` | `""` | S3-compatible endpoint, e.g. `http://minio:9000` or `s3.us-east-1.amazonaws.com`. Required when `enabled=true`. |
| `backups.s3.region` | `us-east-1` | S3 region. |
| `backups.s3.bucket` | `""` | Bucket name. Required when `enabled=true`. NIS calls HeadBucket at startup and fails-fast on a missing bucket — never creates one. |
| `backups.s3.access_key_id` | `""` | Bearer credential. Keep this out of committed config. |
| `backups.s3.secret_access_key` | `""` | Same. |
| `backups.s3.use_path_style` | `true` | Required for MinIO / Garage. AWS S3 supports both; virtual-hosted-style is the default for AWS but path-style works there too. |
| `backups.s3.use_ssl` | `false` | true = https, false = http. Dev MinIO is plain http. |
| `backups.s3.object_prefix` | `""` | Optional path prefix prepended to every object key, e.g. `prod/` to share a bucket across environments. |

### Per-operator commands

```bash
nisctl operator backup enable  <name> --interval 24h --retain 30
nisctl operator backup disable <name>
nisctl operator backup run     <name>           # manual one-shot
nisctl operator backup list    <name>
nisctl operator backup download <BACKUP_ID> -o backup.age
nisctl operator backup delete  <BACKUP_ID>
nisctl operator backup settings <name>          # show current config

# P15 — age recipient management. At least one is REQUIRED before any
# scheduled or manual backup will succeed for this operator.
nisctl operator backup add-recipient    <name> --pubkey age1... [--label LABEL]
nisctl operator backup list-recipients  <name>
nisctl operator backup remove-recipient <name> --pubkey age1...   # or --recipient-id UUID
```

The UI surfaces the same controls on the operator detail page under
"Backups" and "Backup recipients" cards.

### Age keypair workflow (P15)

```bash
# 1) Generate a local keypair. NIS never sees the secret half.
nisctl backup keygen -o ~/.config/nis/backup-ops.key
#   → writes AGE-SECRET-KEY-1... (mode 0600)
#   → prints "age public key (recipient): age1..." to stderr

# 2) Register the public key on the operator.
nisctl operator backup add-recipient my-operator \
    --pubkey age1... --label "ops-team"

# 3) Backups now succeed. Download + decrypt + restore:
nisctl operator backup download <BACKUP_ID> -o backup.age
nisctl backup decrypt -i ~/.config/nis/backup-ops.key -f backup.age -o backup.yaml
nisctl restore -f backup.yaml

# Or in one step (decrypt-then-restore in-process):
nisctl restore -f backup.age -i ~/.config/nis/backup-ops.key
```

**Multi-recipient is the canonical shape.** Register the ops team's key,
the DR location's key, and any per-engineer keys all at once — each can
independently decrypt without sharing identities.

`nisctl backup keygen` refuses to write the secret key to a TTY unless you
pass `--force-stdout` AND redirect (`> file` or `| cmd`). Same for
`nisctl backup decrypt` — refuses to emit plaintext YAML to a TTY because
the YAML contains the operator's full identity tree.

### docker-compose dev setup

`docker-compose.yml` includes a MinIO service and a one-shot `minio-setup`
container that creates the `nis-backups` bucket. NIS is configured to point
at it. **The pinned MinIO image is `RELEASE.2025-04-22T22-12-26Z` — the
last image from the OSS `minio/minio` repo before it was archived on
2026-04-25.** It still works for dev/CI but receives no further updates.
Production deployments should target an actively-maintained S3-compatible
backend: AWS S3, Cloudflare R2, Backblaze B2, [Garage](https://garagehq.deuxfleurs.fr/),
or MinIO's commercial AIStor. NIS uses only the standard S3 API surface
(PutObject / GetObject / HeadObject / ListObjects / RemoveObject /
HeadBucket); any compliant backend works.

### Restore path

Scheduled-backup artifacts are age-encrypted; restoring requires the
matching age identity (secret key):

```bash
nisctl operator backup download <BACKUP_ID> -o backup.age
nisctl restore -f backup.age --identity ~/.config/nis/backup-ops.key
```

The `restore` command sniffs the age header — passing `--identity` on a
plaintext file (or omitting it on an age-encrypted file) is a hard error
rather than a silent mismatch. Manual exports created via
`nisctl backup operator` (separate from scheduled backups) remain plain
YAML/JSON and do not need `--identity`; `restore` auto-detects format.

Scheduled backups carry plaintext NKey seeds inside the age envelope, so
restoring into a *fresh* NIS instance with a different `ENCRYPTION_KEY`
works — the importer re-encrypts each seed against the destination's key
on the fly (`adoptSeed`). Holding any registered age identity is
sufficient; the data encryption key of the original NIS is not required.
This is the disaster-recovery property the backup feature exists to
provide: total wipe of the source instance is survivable as long as you
still hold an age private key.

The corollary: anyone who can decrypt the age envelope reads the seeds in
cleartext. Treat the age private keys with the same care as the operator
NKeys themselves — they are equivalent in blast radius. Pre-2026-05-25
backups stored seeds as `encrypted:keyid:<ciphertext>` references and
still need the source `ENCRYPTION_KEY` to restore; `adoptSeed` handles
either shape transparently per row, so a mixed history of old + new
backups continues to work.

### v1 limitations

- Single global S3 backend across all operators. Per-operator (or per-org)
  S3 routing is a v1.1 design call.
- P15 (2026-05-25) replaced the unencrypted-artifact gap with mandatory
  age encryption. Scheduled backups subsequently flipped to
  `SecretsPlaintext` mode (2026-05-25) so seeds inside the age envelope
  re-encrypt against the destination's key on restore — the age private
  key is the sole gate, the source instance's data encryption key is no
  longer needed. Bucket-level SSE remains useful defense-in-depth.
- The sweeper enumerates operators every `sweep_interval_seconds` in a
  single pass. With thousands of opt-in operators on a single NIS this
  may become a hot loop; per-operator scheduling is a v1.1 ask.
- Per-handler lease for `backup.execute` is 15 minutes. An operator
  whose backup legitimately exceeds that gets reclaimed by another
  worker and produces a duplicate S3 object, which retention eventually
  trims. Multi-GB operators may want to tune the substrate lease via a
  PR — file an issue if you hit this in practice.

### Metrics

- `nis_backups_succeeded_total{trigger=scheduled|manual}` — counter of
  successful uploads.
- `nis_backups_failed_total{trigger}` — counter of failures at any phase
  (export / S3 upload / DB write).
- `nis_backup_duration_seconds{trigger}` — histogram of end-to-end time
  per backup.

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
| `nis_operators_total`, `nis_accounts_total`, `nis_users_total`, `nis_scoped_keys_total`, `nis_clusters_total` | gauge | — | Entity inventory. Refreshed on the `metrics.domain_gauge_refresh_seconds` cadence (default 60s), served from an in-memory cache (no live `COUNT(*)` per scrape). |
| `nis_clusters_healthy` | gauge | — | Clusters last reported healthy by the cluster health-check loop (`cluster.health_check_interval_seconds`, default 60s). |
| `nis_cluster_sync_duration_seconds` | histogram | `outcome` | Duration of `SyncCluster` operations. `outcome` is `ok` / `err`. |
| `nis_cluster_sync_errors_total` | counter | `phase` | Sync errors broken down by where they happened (`open_cluster`, `list_accounts`, …). |
| `nis_cluster_health_check_failures_total` | counter | — | Health-check loop saw a cluster fail to connect or lack credentials. |
| `nis_encryption_failures_total` | counter | `op` | `op` is `encrypt` / `decrypt`. A decrypt-failure spike usually means a key-rotation problem — alert on this. |
| `nis_auth_rejections_total` | counter | `reason` | RPC rejected by the auth interceptor. `reason` ∈ `missing_token`, `invalid_token`, `forbidden`, `invalid_api_token`. |
| `nis_api_token_authentications_total` | counter | `status` | API-token auth outcomes. `status` ∈ `success`, `invalid`, `expired`, `revoked`. |
| `nis_events_emitted_total` | counter | `type` | Events appended to the audit log, by event type (e.g. `account.created`). |
| `nis_webhook_deliveries_total` | counter | `status` | Webhook deliveries by terminal status (`succeeded`/`failed`/`dead_letter`). |
| `nis_webhook_delivery_duration_seconds` | histogram | — | Per-attempt POST latency. |
| `nis_jobs_enqueued_total` | counter | `type` | Background jobs enqueued, by type. EnsureScheduled inserts that lost the watchdog race do NOT count. |
| `nis_jobs_completed_total` | counter | `type`, `outcome` | Background jobs reaching a terminal handler outcome. `outcome` ∈ `succeeded`, `failed`, `dead_lettered`. `failed` is a transient failure that will retry; `dead_lettered` is permanent. |
| `nis_job_duration_seconds` | histogram | `type` | Per-handler-invocation runtime. No `worker_id` label — would explode cardinality in containers where pid varies. |
| `nis_user_jwt_revocations_purged_total` | counter | — | Rows hard-deleted by `revocations.retention_sweep` (P14). Distinct from `_pruned_total`, which counts the soft-prune step (`MarkPruned`) performed by `jwt.expiry_sweep` — pruned rows still exist on disk; purged rows are gone. |

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

- **Quick start**: [README.md#quick-start](#quick-start)
- **Dev Guide**: [CLAUDE.md](CLAUDE.md)
- **Docker Hub**: https://hub.docker.com/r/mauricethomas/nis
- **NATS JWT Docs**: https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt

## License

MIT
