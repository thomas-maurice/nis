# NATS Identity Service - Operations Guide

Operational procedures for running NIS in production. This document covers encryption key rotation, backup and restore, database migrations, monitoring, and troubleshooting.

---

## Table of Contents

1. [Encryption Key Rotation](#encryption-key-rotation)
2. [Backup & Restore](#backup--restore)
3. [Database Migrations](#database-migrations)
4. [Monitoring](#monitoring)
5. [Troubleshooting](#troubleshooting)

---

## Encryption Key Rotation

NIS encrypts all private keys at rest using ChaCha20-Poly1305. The encryption configuration supports multiple keys simultaneously, allowing zero-downtime rotation.

### How It Works

- All keys listed in `encryption.keys` are available for **decryption**.
- Only the key referenced by `encryption.current_key_id` is used for **new encryptions**.
- Old data remains readable as long as the old key stays in the key list.

### Step 1: Generate a New Key

```bash
openssl rand -base64 32
```

This produces a 32-byte key encoded as base64 (e.g., `ZTNhOGY0YjdjOWQxMjM0NTY3ODlhYmNk`).

### Step 2: Add the New Key to Configuration

Edit `config.yaml` and add the new key to the `keys` list. Keep all existing keys in place.

```yaml
encryption:
  current_key_id: "key-2025-01"   # still pointing to the old key
  keys:
    - id: "key-2025-01"
      key: "OLD_KEY_BASE64_HERE"
    - id: "key-2025-02"
      key: "NEW_KEY_BASE64_HERE"   # newly generated key
```

### Step 3: Update `current_key_id`

Change `current_key_id` to point to the new key:

```yaml
encryption:
  current_key_id: "key-2025-02"   # now using the new key
  keys:
    - id: "key-2025-01"
      key: "OLD_KEY_BASE64_HERE"
    - id: "key-2025-02"
      key: "NEW_KEY_BASE64_HERE"
```

### Step 4: Restart the Service

```bash
# Binary
pkill -f "bin/nis serve"
./bin/nis serve --config config.yaml &

# Docker Compose
docker-compose restart nis
```

After restart:
- New encryptions use the new key.
- Existing encrypted data is still decryptable via the old key.

### Step 5 (Optional): Re-encrypt Existing Data

To fully retire the old key, re-encrypt all data so it uses the new key. After re-encryption completes, the old key can be safely removed from the configuration.

**Important:** Do not remove old keys from the config until all data has been re-encrypted. If you remove a key while data still references it, those records become unreadable.

### Key Rotation Checklist

- [ ] Generate new key with `openssl rand -base64 32`
- [ ] Add new key to `encryption.keys` in config
- [ ] Update `current_key_id` to new key ID
- [ ] Back up database before restart
- [ ] Restart NIS service
- [ ] Verify service is healthy (`/healthz` returns 200)
- [ ] (Optional) Re-encrypt existing data
- [ ] (Optional) Remove old key from config after full re-encryption

---

## Backup & Restore

### SQLite

SQLite stores the entire database in a single file. The default path is `./nis.db` (or `/data/nis.db` inside Docker).

#### Backup

```bash
# Simple file copy (stop the service first for consistency)
cp nis.db nis.db.backup-$(date +%Y%m%d-%H%M%S)

# Online backup using SQLite CLI (no downtime required)
sqlite3 nis.db ".backup '/path/to/backup/nis.db.backup'"

# With timestamp
sqlite3 nis.db ".backup '/path/to/backup/nis-$(date +%Y%m%d-%H%M%S).db'"
```

#### Restore

```bash
# Stop the service
pkill -f "bin/nis serve"
# or: docker-compose stop nis

# Replace the database file
cp /path/to/backup/nis.db.backup nis.db

# Restart the service
./bin/nis serve --config config.yaml
# or: docker-compose start nis
```

### PostgreSQL

When using PostgreSQL as the database backend, use standard `pg_dump` and `pg_restore` tools.

#### Backup

```bash
# Full dump (custom format, compressed)
pg_dump -h localhost -U nis -d nis -Fc -f nis-backup-$(date +%Y%m%d-%H%M%S).dump

# SQL format (human-readable)
pg_dump -h localhost -U nis -d nis -f nis-backup-$(date +%Y%m%d-%H%M%S).sql

# Schema only
pg_dump -h localhost -U nis -d nis --schema-only -f nis-schema.sql

# Data only
pg_dump -h localhost -U nis -d nis --data-only -f nis-data.sql
```

#### Restore

```bash
# From custom format dump
pg_restore -h localhost -U nis -d nis --clean --if-exists nis-backup.dump

# From SQL format
psql -h localhost -U nis -d nis < nis-backup.sql

# Create a fresh database and restore
dropdb -h localhost -U nis nis
createdb -h localhost -U nis nis
pg_restore -h localhost -U nis -d nis nis-backup.dump
```

### Docker Volume Backup

When running with Docker Compose, data lives in named volumes (`nis_data`, `nats_data`).

#### Backup Volumes

```bash
# Backup NIS data volume
docker run --rm \
  -v nis_data:/source:ro \
  -v $(pwd)/backups:/backup \
  alpine tar czf /backup/nis-data-$(date +%Y%m%d-%H%M%S).tar.gz -C /source .

# Backup NATS data volume
docker run --rm \
  -v nats_data:/source:ro \
  -v $(pwd)/backups:/backup \
  alpine tar czf /backup/nats-data-$(date +%Y%m%d-%H%M%S).tar.gz -C /source .

# Backup both volumes
mkdir -p backups
for vol in nis_data nats_data; do
  docker run --rm \
    -v ${vol}:/source:ro \
    -v $(pwd)/backups:/backup \
    alpine tar czf /backup/${vol}-$(date +%Y%m%d-%H%M%S).tar.gz -C /source .
done
```

#### Restore Volumes

```bash
# Stop services first
docker-compose down

# Restore NIS data volume
docker run --rm \
  -v nis_data:/target \
  -v $(pwd)/backups:/backup:ro \
  alpine sh -c "rm -rf /target/* && tar xzf /backup/nis-data-TIMESTAMP.tar.gz -C /target"

# Restore NATS data volume
docker run --rm \
  -v nats_data:/target \
  -v $(pwd)/backups:/backup:ro \
  alpine sh -c "rm -rf /target/* && tar xzf /backup/nats-data-TIMESTAMP.tar.gz -C /target"

# Restart services
docker-compose up -d
```

### Backup Schedule Recommendations

| Environment | Frequency | Retention |
|---|---|---|
| Development | On-demand | 1 week |
| Staging | Daily | 2 weeks |
| Production | Every 6 hours | 30 days |

Always back up the database **before** encryption key rotation, service upgrades, or migration runs.

---

## Database Migrations

NIS uses [goose](https://github.com/pressly/goose) for database migrations. Migration files are in the `migrations/` directory.

### Automatic Migrations

By default, migrations run automatically on server start when `auto_migrate` is enabled:

```yaml
# config.yaml
auto_migrate: true
```

Or via environment variable:

```bash
DATABASE_AUTO_MIGRATE=true
```

**Note:** Automatic migrations are convenient for development but should be used cautiously in production. Prefer explicit migration runs in production environments.

### Manual Migration with Goose

#### Install Goose

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

#### Run Migrations (SQLite)

```bash
# Apply all pending migrations
goose -dir migrations sqlite3 nis.db up

# Apply one migration at a time
goose -dir migrations sqlite3 nis.db up-by-one

# Check current migration status
goose -dir migrations sqlite3 nis.db status

# Check current version
goose -dir migrations sqlite3 nis.db version
```

#### Run Migrations (PostgreSQL)

```bash
# Apply all pending migrations
goose -dir migrations postgres "host=localhost user=nis password=secret dbname=nis sslmode=disable" up

# Check status
goose -dir migrations postgres "host=localhost user=nis password=secret dbname=nis sslmode=disable" status
```

### Rollback Procedure

```bash
# Roll back the last migration
goose -dir migrations sqlite3 nis.db down

# Roll back to a specific version
goose -dir migrations sqlite3 nis.db down-to 00001
```

### Pre-Migration Checklist

1. **Back up the database** before running any migration:
   ```bash
   # SQLite
   cp nis.db nis.db.pre-migration-$(date +%Y%m%d-%H%M%S)

   # PostgreSQL
   pg_dump -h localhost -U nis -d nis -Fc -f nis-pre-migration-$(date +%Y%m%d-%H%M%S).dump
   ```

2. **Review the migration SQL** in the `migrations/` directory to understand what changes will be applied.

3. **Test the migration** on a copy of the database or in a staging environment first.

4. **Plan for downtime** if the migration includes schema changes that could lock tables.

### Recovery from Failed Migration

If a migration fails partway through:

```bash
# Check which version the database is at
goose -dir migrations sqlite3 nis.db version

# Attempt rollback
goose -dir migrations sqlite3 nis.db down

# If rollback also fails, restore from backup
pkill -f "bin/nis serve"
cp nis.db.pre-migration-TIMESTAMP nis.db
./bin/nis serve --config config.yaml
```

For SQLite, if the database is corrupted:

```bash
# Check integrity
sqlite3 nis.db "PRAGMA integrity_check;"

# If corrupted, restore from backup
rm nis.db
cp nis.db.pre-migration-TIMESTAMP nis.db
```

---

## Monitoring

### Health Check Endpoint

NIS exposes a health check endpoint at `/healthz`.

```bash
# Basic health check
curl -s http://localhost:8080/healthz
# Returns: "ok" with HTTP 200 when healthy

# With status code
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/healthz
# Returns: 200 when healthy, 503 when migrations are still running
```

The Docker image includes a built-in health check:

```dockerfile
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1
```

### NATS Monitoring

NATS exposes monitoring data on port 8222:

```bash
# Server info
curl http://localhost:8222/varz

# Connection info
curl http://localhost:8222/connz

# Subscription info
curl http://localhost:8222/subsz

# JetStream info
curl http://localhost:8222/jsz

# Route info (clustering)
curl http://localhost:8222/routez
```

### Suggested Prometheus Metrics

If integrating with Prometheus, consider tracking these metrics:

```yaml
# NIS application metrics
- nis_operators_total           # Total number of operators
- nis_accounts_total            # Total number of accounts
- nis_users_total               # Total number of users
- nis_clusters_total            # Total number of registered clusters
- nis_clusters_healthy          # Number of healthy clusters
- nis_cluster_sync_duration_seconds   # Time to sync accounts to a cluster
- nis_cluster_sync_errors_total       # Number of failed sync operations
- nis_encryption_operations_total     # Encryption/decryption operations
- nis_api_request_duration_seconds    # API request latency histogram
- nis_api_requests_total              # Total API requests by method and status

# Database metrics
- nis_db_connections_open       # Open database connections
- nis_db_query_duration_seconds # Query latency histogram
```

### Suggested Alerting Rules

```yaml
# Prometheus alerting rules (example)
groups:
  - name: nis-alerts
    rules:
      # NIS service is down
      - alert: NISServiceDown
        expr: up{job="nis"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "NIS service is down"
          description: "NIS has been unreachable for more than 1 minute."

      # Health check failing
      - alert: NISHealthCheckFailing
        expr: probe_success{job="nis-healthcheck"} == 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "NIS health check is failing"

      # NATS cluster unhealthy
      - alert: NATSClusterUnhealthy
        expr: nis_clusters_healthy < nis_clusters_total
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "One or more NATS clusters are unhealthy"
          description: "{{ $value }} clusters are unhealthy for more than 5 minutes."

      # Cluster sync failures
      - alert: NISClusterSyncFailing
        expr: rate(nis_cluster_sync_errors_total[5m]) > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Cluster sync operations are failing"

      # High API error rate
      - alert: NISHighErrorRate
        expr: rate(nis_api_requests_total{status=~"5.."}[5m]) / rate(nis_api_requests_total[5m]) > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "NIS API error rate exceeds 5%"

      # Database connection issues
      - alert: NISDBConnectionPoolExhausted
        expr: nis_db_connections_open >= nis_db_connections_max
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Database connection pool is exhausted"
```

### External Health Check (cron-based)

For environments without Prometheus, a simple cron-based check:

```bash
#!/bin/bash
# /usr/local/bin/nis-healthcheck.sh
STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/healthz)
if [ "$STATUS" != "200" ]; then
  echo "$(date): NIS health check failed with status $STATUS" >> /var/log/nis-healthcheck.log
  # Send alert (email, Slack webhook, PagerDuty, etc.)
fi
```

```cron
# Check every minute
* * * * * /usr/local/bin/nis-healthcheck.sh
```

---

## Scaling Considerations

### Single Instance (SQLite)

SQLite is the default database driver. It stores everything in a single file and requires no external dependencies.

- Suitable for development, small teams, and single-server deployments
- Supports one writer at a time (readers are concurrent)
- Cannot run multiple NIS instances against the same SQLite file
- Backup is a simple file copy
- No connection pooling needed

### Multi-Instance (PostgreSQL)

For production deployments requiring high availability or multiple NIS instances, use PostgreSQL.

- Supports concurrent reads and writes from multiple NIS instances
- All instances share the same database
- All instances must have identical encryption key configurations
- Use connection pooling (PgBouncer) for high connection counts

```yaml
database:
  driver: "postgres"
  host: "db.example.com"
  port: 5432
  user: "nis"
  password: "strong-password"
  dbname: "nis"
  sslmode: "require"
```

### Load Balancing

When running multiple NIS instances:

- Place instances behind a standard HTTP load balancer (nginx, HAProxy, ALB)
- Health check background tasks (cluster monitoring) run independently on each instance; this is safe and does not cause conflicts
- Session affinity is not required since authentication is token-based

---

## Troubleshooting

### Port Already in Use

**Symptom:** Service fails to start with "address already in use" or "bind: address already in use".

**Solution:**

```bash
# Find what is using port 8080
lsof -ti:8080

# Kill the process
lsof -ti:8080 | xargs kill -9

# Or use a different port
./bin/nis serve --address :9090 --config config.yaml
```

### NATS Unreachable

**Symptom:** Cluster sync fails, cluster health checks report unhealthy, or "connection refused" errors.

**Diagnosis:**

```bash
# Check if NATS is running
docker ps | grep nats

# Check NATS logs
docker logs nis-nats --tail 50

# Test connectivity from the NIS host
nats server ping --server nats://localhost:4222

# Test with curl (NATS monitoring port)
curl -s http://localhost:8222/varz | head -5

# If using Docker, check network connectivity between containers
docker exec nis-server wget -q -O- http://nats:8222/varz | head -5
```

**Common Causes:**

- NATS container is not running: `docker-compose up -d nats`
- Wrong URL in cluster configuration (use `nats://nats:4222` for Docker networking, `nats://localhost:4222` for host)
- Firewall blocking port 4222
- NATS crashed due to misconfiguration: check `docker logs nis-nats`

### Encryption Key Mismatch

**Symptom:** Errors like "decryption failed", "cipher: message authentication failed", or garbled data when reading operators/accounts/users.

**Diagnosis:**

```bash
# Verify your config has the correct keys
grep -A 20 "encryption:" config.yaml

# Check if the current_key_id matches one of the listed keys
grep "current_key_id" config.yaml
grep "id:" config.yaml
```

**Common Causes:**

- The encryption key used to encrypt existing data was removed from the config.
- The `current_key_id` references a key ID that does not exist in the `keys` list.
- The encryption key value was changed (rotated) without keeping the old key for decryption.
- Using `--encryption-key` flag (single key mode) after previously using multi-key config, or vice versa.

**Solution:**

1. Ensure all keys that were ever used to encrypt data are present in `encryption.keys`.
2. Verify `current_key_id` matches an existing key entry.
3. If the original key is lost, the encrypted data cannot be recovered. Restore from backup.

### Migration Failures

**Symptom:** Service fails to start with migration errors, or `goose` commands fail.

**Diagnosis:**

```bash
# Check current migration version
goose -dir migrations sqlite3 nis.db version

# Check migration status
goose -dir migrations sqlite3 nis.db status

# Check database integrity (SQLite)
sqlite3 nis.db "PRAGMA integrity_check;"
```

**Common Causes and Solutions:**

| Cause | Solution |
|---|---|
| Database file permissions | `chmod 644 nis.db` and ensure the NIS user owns the file |
| Database locked by another process | Stop all NIS instances, remove WAL files: `rm -f nis.db-shm nis.db-wal` |
| Corrupt database | Restore from backup |
| Incompatible migration | Roll back with `goose -dir migrations sqlite3 nis.db down`, fix the issue, then re-apply |

**Nuclear option** (development only):

```bash
# Delete the database and let NIS recreate it
rm nis.db
./bin/nis serve --config config.yaml
```

### Docker Container Fails to Start

**Symptom:** Container exits immediately or keeps restarting.

```bash
# Check container logs
docker logs nis-server

# Check container status
docker-compose ps

# Check if volumes are accessible
docker run --rm -v nis_data:/data alpine ls -la /data

# Rebuild the image
docker-compose build --no-cache nis
docker-compose up -d
```

### Cluster Sync Fails

**Symptom:** `nisctl cluster sync` reports errors or accounts do not appear in NATS.

```bash
# Verify the cluster is registered and has credentials
./bin/nisctl cluster get <cluster-name>

# Check that NATS has the operator loaded
docker logs nis-nats | grep "Trusted Operators"

# Verify the system account credentials work
./bin/nisctl user creds cluster-<cluster-name> \
  --operator <operator-name> \
  --account '$SYS' > /tmp/sys-creds.creds

nats --creds=/tmp/sys-creds.creds --server=nats://localhost:4222 rtt

# Check the NATS resolver directory
ls -la /path/to/resolver/
# or in Docker:
docker exec nis-nats ls -la /resolver/
```

### High Memory or CPU Usage

**Symptom:** NIS or NATS consuming excessive resources.

```bash
# Check resource usage
docker stats nis-server nis-nats

# For non-Docker deployments
ps aux | grep nis
top -p $(pgrep -f "bin/nis serve")

# Check SQLite database size
ls -lh nis.db

# Check NATS JetStream storage
du -sh /path/to/jetstream/
# or in Docker:
docker exec nis-nats du -sh /data/jetstream/
```

### Log Level Adjustment

To get more detailed logs for debugging, change the log level:

```yaml
# config.yaml
logging:
  level: "debug"   # debug, info, warn, error
  format: "json"   # json format is easier to parse with log aggregators
```

Or set via environment:

```bash
LOGGING_LEVEL=debug docker-compose up -d nis
```

---

## Quick Reference

| Task | Command |
|---|---|
| Health check | `curl http://localhost:8080/healthz` |
| View logs | `docker logs nis-server -f` |
| Restart service | `docker-compose restart nis` |
| Backup SQLite | `sqlite3 nis.db ".backup nis.db.bak"` |
| Backup PostgreSQL | `pg_dump -U nis -d nis -Fc -f backup.dump` |
| Run migrations | `goose -dir migrations sqlite3 nis.db up` |
| Rollback migration | `goose -dir migrations sqlite3 nis.db down` |
| Generate encryption key | `openssl rand -base64 32` |
| Check migration status | `goose -dir migrations sqlite3 nis.db status` |
| Kill port 8080 | `lsof -ti:8080 \| xargs kill -9` |
