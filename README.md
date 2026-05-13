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
go test ./...     # Run tests
```

Convenience targets:

| Target | What it does |
|---|---|
| `make run` | Full dev stack: Postgres + NATS (Docker) + NIS (host) + admin user |
| `make run-demo` | `run` + JWT bootstrap (operator, demo cluster/account/user, creds file) |
| `make run-stop` / `run-clean` | Stop / wipe the dev stack |
| `make serve-local` | Legacy host-only server, SQLite, hardcoded dev secrets |
| `make docker-build` / `docker-run` / `docker-stop` | Single-container Docker image lifecycle |

## Production

- Use PostgreSQL for database
- Enable HTTPS (reverse proxy)
- Rotate encryption keys
- Regular backups
- Multiple instances behind load balancer

## Links

- **Quickstart**: [QUICKSTART.md](QUICKSTART.md)
- **Dev Guide**: [CLAUDE.md](CLAUDE.md)
- **Docker Hub**: https://hub.docker.com/r/mauricethomas/nis
- **NATS JWT Docs**: https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt

## License

MIT
