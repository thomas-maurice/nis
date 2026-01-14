# Docker Setup Guide

## Quick Start

### Using Docker Compose (Recommended)

```bash
# Start all services
docker-compose up -d

# Access the UI
open http://localhost:8080

# Login credentials (created automatically)
Username: admin
Password: admin123

# View logs
docker-compose logs -f nis

# Stop all services
docker-compose down

# Stop and remove volumes (fresh start)
docker-compose down -v
```

## Services Included

### Main Stack (`docker-compose.yml`)

1. **PostgreSQL** - Production-ready database
   - Port: 5432
   - Database: nis
   - User: nis
   - Password: nis_password

2. **NIS Server** - Main application server
   - Port: 8080
   - Auto-migration enabled
   - Health checks configured (waits for migrations to complete)
   - Runs as non-root user

3. **NIS Setup** - One-time admin user creation
   - Runs database migrations (idempotent - safe to run multiple times)
   - Creates admin/admin123 automatically with retry logic
   - Runs once and exits
   - Safe to run multiple times (idempotent)

4. **NATS Server** - Message broker with JetStream
   - Client port: 4222
   - Management port: 8222
   - JetStream enabled

## Configuration

Environment variables are set in docker-compose.yml:

```yaml
JWT_SECRET: change-this-to-a-secure-random-secret-at-least-32-bytes-long
ENCRYPTION_KEY: 12345678901234567890123456789012
DB_DRIVER: postgres
DB_DSN: host=postgres user=nis password=nis_password dbname=nis port=5432 sslmode=disable
```

**IMPORTANT:** Change these secrets in production!

## Build Details

### Dockerfile

- **Base Images:**
  - Builder: `golang:1.25-alpine`, `node:22-alpine`
  - Runtime: `alpine:3.21`

- **Build Optimizations:**
  - Multi-stage build (reduces image size)
  - Binary stripping (`-ldflags="-s -w"`)
  - Non-root user (uid/gid 1000)
  - Built-in health checks

- **Security:**
  - Runs as `nis` user (not root)
  - Minimal runtime dependencies
  - No build tools in final image

## Troubleshooting

### Admin user already exists
```bash
# The setup container will show: "user already exists" - this is normal
docker-compose logs nis-setup
```

### Database connection issues
```bash
# Check postgres is healthy
docker-compose ps postgres

# View postgres logs
docker-compose logs postgres
```

### Reset everything
```bash
# Stop and remove all data
docker-compose down -v

# Start fresh
docker-compose up -d
```

### Port conflicts
```bash
# Check what's using the ports
lsof -ti:8080 | xargs kill -9  # NIS
lsof -ti:5432 | xargs kill -9  # PostgreSQL
lsof -ti:4222 | xargs kill -9  # NATS
```

## Production Deployment

1. **Update secrets** in docker-compose.yml
2. **Use environment files** (create `.env`)
3. **Configure backups** for PostgreSQL volume
4. **Set up reverse proxy** (nginx/traefik) with HTTPS
5. **Monitor logs** and health checks
6. **Scale horizontally** (multiple NIS instances behind load balancer)

Example `.env` file:
```bash
JWT_SECRET=your-generated-secret-here
ENCRYPTION_KEY=your-32-byte-encryption-key-here
POSTGRES_PASSWORD=your-postgres-password
```

## Volumes

Persistent data is stored in Docker volumes:

- `postgres_data` - PostgreSQL database
- `nats_data` - NATS JetStream data

Backup these volumes for disaster recovery.
