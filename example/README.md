# NATS Identity Service - Example

This example demonstrates a complete NIS deployment with NATS JWT authentication.

## Quick Start

```bash
# Run automated setup
chmod +x setup.sh
./setup.sh
```

This creates:
- NIS server with Web UI on port 8080
- NATS server with JWT auth on port 4222
- Demo operator, account, and user
- User credentials file (`app-user.creds`)

## What Gets Created

```
┌─────────────────────────────────────┐
│ NIS Server (localhost:8080)         │
│  ├── Operator: demo-operator        │
│  ├── Cluster: demo-cluster          │
│  ├── Account: app-account           │
│  └── User: app-user                 │
└─────────────────────────────────────┘
           ↓
┌─────────────────────────────────────┐
│ NATS Server (localhost:4222)        │
│  ✓ JWT authentication enabled       │
│  ✓ Account JWTs in resolver         │
│  ✓ JetStream enabled                │
└─────────────────────────────────────┘
```

## Manual Setup

If you prefer step-by-step:

```bash
# 1. Start services
docker-compose up -d

# 2. Create admin user
docker exec example-nis ./nis user create admin --password admin123 --role admin

# 3. Get nisctl
docker cp example-nis:/app/nis ./nisctl
chmod +x ./nisctl

# 4. Create operator
./nisctl operator create demo-operator

# 5. Generate NATS config
./nisctl operator generate-include demo-operator > ./data/nats/nats-server.conf

# 6. Restart NATS with JWT auth
docker-compose restart nats

# 7. Create cluster
./nisctl cluster create demo-cluster --operator demo-operator --urls nats://nats:4222

# 8. Create account and user
./nisctl account create app-account --operator demo-operator
./nisctl user create app-user --operator demo-operator --account app-account

# 9. Sync to NATS
./nisctl cluster sync demo-cluster

# 10. Get credentials
./nisctl user creds app-user --operator demo-operator --account app-account > app-user.creds
```

## Test Connection

```bash
# Using NATS CLI
nats --creds=./app-user.creds --server=nats://localhost:4222 rtt

# Publish/Subscribe
nats --creds=./app-user.creds --server=nats://localhost:4222 pub test.subject "Hello NIS!"
```

## Web UI

Open http://localhost:8080 and login with `admin` / `admin123` to:
- View all operators, accounts, users
- Create new entities
- Download credentials
- Monitor cluster health

## Data Persistence

All data is stored in `./data/`:
```
./data/
├── nis/
│   └── nis.db              # NIS database
└── nats/
    ├── nats-server.conf    # NATS JWT config
    ├── resolver/           # Account JWTs
    └── jetstream/          # JetStream storage
```

## Cleanup

```bash
# Stop services
docker-compose down

# Remove all data
rm -rf ./data/
rm -f nisctl app-user.creds
```

## Production Considerations

For production deployments:

1. **Change secrets**: Update `--jwt-secret` and `--encryption-key` in docker-compose.yml
2. **Use PostgreSQL**: Replace SQLite with PostgreSQL for better concurrency
3. **Enable TLS**: Add reverse proxy (nginx/caddy) with HTTPS
4. **NATS clustering**: Run multiple NATS servers for high availability
5. **Regular backups**: Backup `./data/nis/nis.db` and `./data/nats/`

See the main [QUICKSTART.md](../QUICKSTART.md) for detailed documentation.
