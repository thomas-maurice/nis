# NATS Identity Service - Quick Start Guide

Get a complete NATS Identity Service setup running in minutes with Docker Compose.

## Prerequisites

- Docker & Docker Compose
- `make` (for building UI)
- Node.js 18+ (for building UI)

## What You'll Get

This quickstart sets up:
- ✅ **NIS Server** - API and Web UI on http://localhost:8080
- ✅ **NATS Server** - JWT authentication enabled on port 4222
- ✅ **Persistent Storage** - All data in `./data/` directory
- ✅ **Complete Demo Setup** - Operator, accounts, cluster ready to use

---

## Step 1: Build the UI

```bash
# Build the Vue.js UI
make build-ui
```

This creates the production UI bundle and embeds it in the Go binary.

---

## Step 2: Start the Services

```bash
# Start NIS and NATS with Docker Compose
docker-compose up -d

# Check container status
docker-compose ps
```

**Expected output:**
```
NAME         STATUS
nis-server   Up (healthy)
nis-nats     Up (healthy)
```

**What was started:**
- **nis-server**: API on `:8080`, database at `./data/nis/nis.db`
- **nis-nats**: NATS server on `:4222`, `:8222` (monitoring), data in `./data/nats/`

**Access the Web UI:** Open http://localhost:8080 in your browser

---

## Step 3: Create Admin User

```bash
# Create admin user for the API/UI
docker exec nis-server ./nis user create admin \
  --password admin123 \
  --role admin
```

**Output:**
```
✓ API user created successfully
  Username: admin
  Role:     admin
```

**Login to UI:** Use `admin` / `admin123` at http://localhost:8080

---

## Step 4: Create Operator

The operator is the root of trust. Use the CLI or Web UI:

### Option A: CLI
```bash
./bin/nisctl operator create demo-operator \
  --description "Demo NATS operator"
```

### Option B: Web UI
1. Go to http://localhost:8080 and login
2. Click "Operators" → "Create Operator"
3. Enter name: `demo-operator`
4. Click "Create"

**What was created:**
- ✅ Operator NKey pair (Ed25519)
- ✅ Self-signed operator JWT
- ✅ `$SYS` account (system account for NATS)
- ✅ `system` user in $SYS account

---

## Step 5: Generate NATS Configuration

```bash
# Generate NATS server config with JWT auth
./bin/nisctl operator generate-include demo-operator > ./data/nats/nats-server.conf

# Verify config was created
cat ./data/nats/nats-server.conf
```

**The config includes:**
- Operator JWT (root of trust)
- File resolver configuration
- Preloaded $SYS account JWT
- JetStream enabled

---

## Step 6: Restart NATS with JWT Auth

```bash
# Restart NATS to load the new config
docker-compose restart nats

# Check NATS logs to verify JWT auth is active
docker logs nis-nats --tail 20
```

**Expected in logs:**
```
[INF] Trusted Operators
[INF]   Operator: "demo-operator"
[INF] Managing all jwt in exclusive directory /resolver
[INF] Starting JetStream
[INF] Server is ready
```

---

## Step 7: Register the Cluster

Register the NATS server as a cluster in NIS:

```bash
./bin/nisctl cluster create demo-cluster \
  --operator demo-operator \
  --urls nats://nats:4222 \
  --description "Demo NATS cluster"
```

**Output:**
```
✓ Cluster created successfully
System user automatically created for cluster management
healthy: false → will become true after health check
```

**What happened:**
- Cluster registered with NIS
- User `cluster-demo-cluster` auto-created in $SYS
- Credentials encrypted and stored
- Health checks run every 60 seconds

**Wait for health check (60 seconds):**
```bash
# Check cluster status after ~60 seconds
./bin/nisctl cluster get demo-cluster
```

**Expected:**
```
healthy: true
lasthealthcheck: <recent timestamp>
```

---

## Step 8: Create Application Account

Create an account for your application:

### CLI
```bash
./bin/nisctl account create app-account \
  --operator demo-operator \
  --description "Application account" \
  --max-memory 1073741824 \
  --max-storage 10737418240 \
  --max-streams 10 \
  --max-consumers 100
```

### Web UI
1. Go to "Accounts" → "Create Account"
2. Fill in details and JetStream limits
3. Click "Create"

---

## Step 9: Sync Accounts to NATS

Push all account JWTs to the NATS resolver:

```bash
./bin/nisctl cluster sync demo-cluster
```

**Output:**
```
Syncing accounts to cluster...
✓ Successfully synced 2 accounts to cluster
  - app-account
  - $SYS
```

**What happened:**
1. NIS connected to NATS using encrypted $SYS credentials
2. Retrieved all accounts for the operator
3. Pushed each account JWT via `$SYS.REQ.CLAIMS.UPDATE`
4. NATS wrote JWTs to `./data/nats/resolver/<pubkey>.jwt`

**Verify:**
```bash
ls -la ./data/nats/resolver/
```

---

## Step 10: Create User and Test Connection

Create a user and test authentication:

```bash
# Create user
./bin/nisctl user create app-user \
  --operator demo-operator \
  --account app-account \
  --description "Test user"

# Get credentials file
./bin/nisctl user creds app-user \
  --operator demo-operator \
  --account app-account \
  > /tmp/app-user.creds

# Test connection with NATS CLI (install if needed: brew install nats-io/nats-tools/nats)
nats context save nis --server=nats://localhost:4222 --creds=/tmp/app-user.creds
nats context select nis
nats pub test.hello "Hello from NIS!"
```

**Or test with Go:**

```bash
cat > /tmp/test-nats.go <<'EOF'
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	nc, err := nats.Connect("nats://localhost:4222",
		nats.UserCredentials("/tmp/app-user.creds"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	fmt.Println("✓ Connected to NATS with JWT authentication!")

	// Test pub/sub
	subject := "test.demo"
	sub, _ := nc.SubscribeSync(subject)
	nc.Publish(subject, []byte("Hello from NIS!"))
	msg, _ := sub.NextMsg(2 * time.Second)

	fmt.Printf("✓ Received: %s\n", string(msg.Data))
	fmt.Println("🎉 SUCCESS!")
}
EOF

cd /tmp
go mod init test 2>/dev/null || true
go get github.com/nats-io/nats.go
go run test-nats.go
```

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│ Web Browser → http://localhost:8080                     │
│   ├── Vue.js UI (login: admin/admin123)                 │
│   └── Manage: Operators, Accounts, Users, Clusters      │
└─────────────────────────────────────────────────────────┘
                       ↓
┌─────────────────────────────────────────────────────────┐
│ NIS Server Container (nis-server)                       │
│   ├── API: ConnectRPC on :8080                          │
│   ├── Database: ./data/nis/nis.db (SQLite)              │
│   ├── Encrypted credentials storage                     │
│   └── Cluster health monitoring (every 60s)             │
└─────────────────────────────────────────────────────────┘
                       ↓
                 (cluster sync)
                       ↓
┌─────────────────────────────────────────────────────────┐
│ NATS Server Container (nis-nats)                        │
│   ├── Client port: :4222                                │
│   ├── Monitoring: :8222                                 │
│   ├── JWT Auth: Operator JWT loaded                     │
│   ├── Resolver: ./data/nats/resolver/*.jwt              │
│   └── JetStream: ./data/nats/jetstream/                 │
└─────────────────────────────────────────────────────────┘
                       ↑
                (nats.Connect)
                       ↑
┌─────────────────────────────────────────────────────────┐
│ Your Application                                        │
│   └── Authenticates with user.creds file                │
└─────────────────────────────────────────────────────────┘
```

---

## Data Persistence

All data is stored in `./data/` (git-ignored):

```
./data/
├── nis/
│   └── nis.db              # NIS database (SQLite)
└── nats/
    ├── nats-server.conf    # NATS configuration
    ├── resolver/           # Account JWTs
    │   ├── <pubkey>.jwt
    │   └── <pubkey>.jwt
    └── jetstream/          # JetStream storage
```

**Backup:** Simply backup the `./data/` directory to preserve all state.

---

## Common Commands

### View Logs
```bash
# NIS server logs
docker logs nis-server -f

# NATS logs
docker logs nis-nats -f

# Both
docker-compose logs -f
```

### Restart Services
```bash
# Restart everything
docker-compose restart

# Restart just NATS (after config change)
docker-compose restart nats

# Restart just NIS
docker-compose restart nis
```

### Stop/Start
```bash
# Stop all services
docker-compose down

# Start all services
docker-compose up -d

# Remove all data and start fresh
docker-compose down
rm -rf ./data/nis/* ./data/nats/*
docker-compose up -d
```

### CLI Commands
```bash
# Operators
./bin/nisctl operator list
./bin/nisctl operator get demo-operator
./bin/nisctl operator generate-include demo-operator > nats.conf

# Accounts
./bin/nisctl account list demo-operator
./bin/nisctl account get --operator demo-operator app-account

# Users
./bin/nisctl user list app-account --operator demo-operator
./bin/nisctl user creds app-user --operator demo-operator --account app-account

# Clusters
./bin/nisctl cluster list
./bin/nisctl cluster get demo-cluster
./bin/nisctl cluster sync demo-cluster
```

---

## Web UI Features

Access the UI at http://localhost:8080 (login: `admin` / `admin123`)

### Dashboard
- Entity counts and statistics
- Quick navigation to all sections

### Operators
- Create/edit/delete operators
- View operator JWT and public keys
- Generate NATS server configuration
- Set system account

### Accounts
- Create accounts with JetStream limits
- View account JWTs
- Filter by operator

### Users
- Create users in accounts
- Download `.creds` files
- View user JWTs
- Associate with scoped signing keys

### Clusters
- Register NATS clusters
- View health status (refreshes every 60s)
- Sync accounts to clusters
- View connection details

### Signing Keys
- Create scoped signing keys for delegated permissions
- Set pub/sub allow/deny lists
- Response permissions

---

## Troubleshooting

### Port Already in Use
```bash
# Kill processes on ports
lsof -ti:8080 | xargs kill -9
lsof -ti:4222 | xargs kill -9

# Or use different ports in docker-compose.yml
```

### Database Locked
```bash
# Stop containers and remove database lock
docker-compose down
rm -f ./data/nis/nis.db-shm ./data/nis/nis.db-wal
docker-compose up -d
```

### Cluster Unhealthy
```bash
# Check NATS is running
docker logs nis-nats

# Check credentials are set
./bin/nisctl cluster get demo-cluster | grep -i cred

# Manually test connection with system user
./bin/nisctl user creds cluster-demo-cluster \
  --operator demo-operator \
  --account '$SYS' \
  > /tmp/cluster-creds.creds

nats --creds=/tmp/cluster-creds.creds --server=nats://localhost:4222 rtt
```

### UI Not Loading
```bash
# Rebuild UI and restart
make build-ui
docker-compose build nis
docker-compose up -d nis
```

### NATS Authentication Fails
```bash
# Verify operator is loaded
docker logs nis-nats | grep Operator

# Check account JWT exists in resolver
ls -la ./data/nats/resolver/

# Verify user credentials file format
head -5 /tmp/app-user.creds
```

---

## Production Recommendations

### Security
- Change default admin password immediately
- Use strong JWT secret (32+ bytes): `--jwt-secret`
- Use strong encryption key (32 bytes): `--encryption-key`
- Enable HTTPS with reverse proxy (nginx, caddy)
- Restrict Docker network access
- Use PostgreSQL instead of SQLite

### High Availability
- Run multiple NATS servers in cluster mode
- Use JetStream resolver instead of file resolver
- Deploy NIS behind load balancer
- Regular database backups
- Monitor cluster health

### Monitoring
- NATS monitoring port: http://localhost:8222
- Set up Prometheus metrics
- Alert on cluster health status
- Monitor JWT expiration (if set)

---

## Next Steps

1. **Explore the Web UI** - Create more accounts, users, manage everything visually
2. **Build Applications** - Use the generated `.creds` files in your apps
3. **Add More Clusters** - Register additional NATS servers
4. **Set Up Scoped Keys** - Fine-grained permissions with scoped signing keys
5. **Automate Sync** - Set up cron job to sync accounts periodically
6. **Production Deploy** - Move to PostgreSQL, enable TLS, configure backups

---

## Cleanup

```bash
# Stop and remove everything
docker-compose down

# Remove all data (WARNING: deletes all operators, accounts, users)
rm -rf ./data/

# Remove built binaries
make clean
```

---

## Resources

- **API Documentation**: http://localhost:8080 (when server is running)
- **NATS Docs**: https://docs.nats.io
- **JWT Auth Guide**: https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt
- **GitHub**: https://github.com/thomas-maurice/nis

---

**🎉 You now have a fully functional NATS Identity Service!**

Use the Web UI to manage everything, or use `nisctl` for automation and CI/CD integration.
