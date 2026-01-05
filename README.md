# NATS Identity Service (NIS)

**Centralized identity management for NATS with JWT authentication**

NIS is a complete identity management solution for NATS servers that simplifies JWT-based authentication. It provides a RESTful API, CLI, and Web UI to manage operators, accounts, users, and scoped signing keys - with automatic credential encryption, cluster health monitoring, and seamless NATS integration.

## Features

- 🔐 **JWT Authentication**: Full NATS operator/account/user JWT lifecycle management
- 🌐 **Web UI**: Modern Vue.js interface for visual management
- 🛠️ **CLI Tool**: `nisctl` for automation and CI/CD integration
- 🔒 **Encrypted Storage**: ChaCha20-Poly1305 encryption for sensitive credentials
- 📊 **Cluster Monitoring**: Automatic health checks for registered NATS clusters
- 🔄 **Dynamic Sync**: Push account JWTs to NATS via `$SYS.REQ.CLAIMS.UPDATE`
- 🚀 **JetStream Support**: Configure per-account resource limits
- 🔑 **Scoped Signing Keys**: Delegated JWT signing with fine-grained permissions
- 🐳 **Docker Ready**: Production-ready Docker images and compose files

## Quick Start

### Using Docker (Recommended)

```bash
# Clone and navigate to example
git clone https://github.com/thomas-maurice/nis
cd nis/example

# Run automated setup
chmod +x setup.sh
./setup.sh

# Access Web UI
open http://localhost:8080
# Login: admin / admin123
```

This creates a complete working environment with NIS + NATS in under 2 minutes.

### Using Binary

```bash
# Download latest release
curl -L https://github.com/thomas-maurice/nis/releases/latest/download/nis -o nis
curl -L https://github.com/thomas-maurice/nis/releases/latest/download/nisctl -o nisctl
chmod +x nis nisctl

# Start server
./nis serve --jwt-secret "your-secret-min-32-bytes" --encryption-key "exactly-32-bytes-for-encryption"

# Create entities
./nisctl operator create my-operator
./nisctl account create my-account --operator my-operator
./nisctl user create my-user --operator my-operator --account my-account
```

See [QUICKSTART.md](QUICKSTART.md) for detailed instructions.

## How It Works

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│ Web UI / CLI / API                                       │
│  └─ Create operators, accounts, users                   │
└─────────────────────────────────────────────────────────┘
                       ↓
┌─────────────────────────────────────────────────────────┐
│ NIS Server (Go + gRPC)                                   │
│  ├─ Generate NKeys (Ed25519)                             │
│  ├─ Sign JWTs (operators, accounts, users)               │
│  ├─ Encrypt credentials (ChaCha20-Poly1305)              │
│  ├─ Store in database (SQLite/PostgreSQL)                │
│  └─ Monitor cluster health                               │
└─────────────────────────────────────────────────────────┘
                       ↓
┌─────────────────────────────────────────────────────────┐
│ NATS Server                                              │
│  ├─ Operator JWT loaded (root of trust)                 │
│  ├─ File/JetStream resolver for account JWTs            │
│  ├─ Validate user JWTs on connection                    │
│  └─ Enforce account limits & permissions                │
└─────────────────────────────────────────────────────────┘
```

### JWT Hierarchy

```
Operator (root of trust)
  └─ System Account ($SYS)
       └─ System User (for cluster management)
  └─ Application Accounts
       └─ Users
            └─ Credentials (.creds file)
```

### What NIS Does

1. **Manages JWT Lifecycle**
   - Creates operator JWTs (self-signed, root of trust)
   - Creates account JWTs (signed by operator)
   - Creates user JWTs (signed by account or scoped key)
   - Automatically creates $SYS account for each operator
   - Automatically creates cluster management users

2. **Encrypts Sensitive Data**
   - All NKeys (private keys) encrypted at rest
   - Cluster credentials encrypted in database
   - User credentials encrypted until export

3. **Syncs with NATS**
   - Pushes account JWTs to NATS resolver
   - Uses `$SYS.REQ.CLAIMS.UPDATE` for dynamic updates
   - No NATS restart needed when adding accounts

4. **Monitors Clusters**
   - Periodic health checks (configurable interval)
   - Validates connectivity with encrypted credentials
   - Tracks cluster status in UI/API

## Key Concepts

### Operators
Root of trust for NATS JWT authentication. Each operator has:
- Ed25519 signing key (encrypted)
- Self-signed JWT
- System account ($SYS) for management

### Accounts
Multi-tenant boundaries in NATS. Each account has:
- Public key (identity)
- JWT signed by operator
- Optional JetStream resource limits
- Isolated message space

### Users
Connection credentials for applications. Each user has:
- Public/private key pair (NKey)
- JWT signed by account
- Credentials file (.creds) for NATS clients
- Optional pub/sub permissions

### Scoped Signing Keys
Delegated signing keys for accounts. Enables:
- Fine-grained permission control
- Separate keys for different services
- Pub/sub allow/deny lists
- Response permissions

### Clusters
Registered NATS servers. NIS:
- Auto-creates management user in $SYS
- Stores encrypted credentials
- Monitors health via NATS connection
- Syncs account JWTs dynamically

## Use Cases

### Multi-Tenant SaaS Platform
```bash
# Create operator for platform
nisctl operator create platform-operator

# Create account per customer
nisctl account create customer-1 --operator platform-operator
nisctl account create customer-2 --operator platform-operator

# Create users for each customer's services
nisctl user create customer-1-api --operator platform-operator --account customer-1
nisctl user create customer-2-web --operator platform-operator --account customer-2

# Sync to NATS cluster
nisctl cluster sync production-cluster
```

### Microservices with Service-Specific Keys
```bash
# Create account for microservices
nisctl account create microservices --operator my-operator

# Create scoped keys for each service
nisctl scoped-key create api-service \
  --account microservices \
  --pub-allow "api.>" \
  --sub-allow "api.requests.>"

nisctl scoped-key create worker-service \
  --account microservices \
  --pub-allow "jobs.>" \
  --sub-allow "jobs.queue.>"
```

## Components

### NIS Server
- **Language**: Go
- **API**: Connect-RPC (gRPC-compatible HTTP/JSON)
- **Database**: SQLite (dev) or PostgreSQL (prod)
- **Auth**: JWT-based API authentication with Casbin RBAC
- **UI**: Embedded Vue.js SPA

### nisctl CLI
- **Language**: Go
- **Protocol**: Connect-RPC client
- **Config**: `~/.config/nisctl/config.yaml`
- **Auth**: Token-based (login command)

### Web UI
- **Framework**: Vue 3 + Bootstrap 5
- **Features**:
  - Dashboard with entity counts
  - Full CRUD for all entities
  - Credentials download
  - Cluster health monitoring
  - JWT/NKey visualization

## Configuration

### Server Configuration

Via flags:
```bash
./nis serve \
  --address :8080 \
  --db-dsn ./nis.db \
  --jwt-secret "your-32-byte-secret" \
  --encryption-key "your-32-byte-key"
```

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

## API Examples

### Create Operator
```bash
curl -X POST http://localhost:8080/nis.v1.OperatorService/CreateOperator \
  -H "Content-Type: application/json" \
  -d '{"name":"my-operator","description":"Production operator"}'
```

### Create Account
```bash
curl -X POST http://localhost:8080/nis.v1.AccountService/CreateAccount \
  -H "Content-Type: application/json" \
  -d '{
    "operatorId":"<operator-id>",
    "name":"app-account",
    "jetstreamLimits":{
      "enabled":true,
      "maxMemory":1073741824,
      "maxStorage":10737418240
    }
  }'
```

## Development

### Build from Source

```bash
# Build server + CLI
make build-all

# Build UI only
make build-ui

# Run tests
go test ./...

# Clean build artifacts
make clean
```

### Project Structure

```
nis/
├── cmd/
│   ├── nis/          # Server binary
│   └── nisctl/       # CLI binary
├── internal/
│   ├── application/  # Business logic
│   ├── domain/       # Entities & interfaces
│   ├── infrastructure/  # Database, encryption, NATS
│   └── interfaces/   # gRPC handlers, HTTP UI
├── ui/               # Vue.js frontend
├── proto/            # Protobuf definitions
├── migrations/       # Database migrations
└── example/          # Docker compose example
```

## Docker Images

Pre-built images available at `mauricethomas/nis`:

```bash
# Pull latest
docker pull mauricethomas/nis:latest

# Run server
docker run -p 8080:8080 \
  -e JWT_SECRET="your-secret" \
  -e ENCRYPTION_KEY="your-key" \
  mauricethomas/nis:latest
```

## Production Deployment

### Security Checklist
- ✅ Use strong JWT secret (32+ bytes random)
- ✅ Use strong encryption key (exactly 32 bytes)
- ✅ Enable HTTPS (reverse proxy)
- ✅ Use PostgreSQL instead of SQLite
- ✅ Regular database backups
- ✅ Restrict network access
- ✅ Rotate encryption keys periodically
- ✅ Monitor cluster health alerts

### High Availability
- Deploy multiple NIS instances behind load balancer
- Use PostgreSQL with replication
- Run multiple NATS servers in cluster mode
- Use JetStream resolver for replicated account JWTs

### Scaling
- NIS is stateless (except database)
- Horizontal scaling via load balancer
- Database connection pooling
- Cluster health checks run asynchronously

## Troubleshooting

### Cluster Shows Unhealthy
```bash
# Check NATS is running
docker logs <nats-container>

# Verify credentials exist
nisctl cluster get <cluster-name>

# Test manual connection
nisctl user creds cluster-<name> --account '$SYS' > test.creds
nats --creds=test.creds --server=<nats-url> rtt
```

### Account Not Found in NATS
```bash
# Re-sync accounts
nisctl cluster sync <cluster-name>

# Check resolver directory
ls /path/to/resolver/*.jwt

# Verify NATS config
docker logs <nats-container> | grep Operator
```

### UI Not Loading
```bash
# Rebuild UI
make build-ui

# Check UI is embedded
ls internal/interfaces/http/ui/dist/

# Verify server logs
docker logs <nis-container>
```

## Contributing

Contributions welcome! Please:
1. Fork the repository
2. Create feature branch (`git checkout -b feature/amazing`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing`)
5. Open Pull Request

## License

MIT License - see [LICENSE](LICENSE) file

## Links

- **Documentation**: [QUICKSTART.md](QUICKSTART.md)
- **Docker Hub**: https://hub.docker.com/r/mauricethomas/nis
- **Issues**: https://github.com/thomas-maurice/nis/issues
- **NATS Docs**: https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt

## Acknowledgments

Built with:
- [NATS](https://nats.io) - Cloud-native messaging system
- [nkeys](https://github.com/nats-io/nkeys) - Ed25519 key pairs for NATS
- [Connect-RPC](https://connectrpc.com/) - Modern RPC framework
- [Vue.js](https://vuejs.org/) - Progressive JavaScript framework
- [GORM](https://gorm.io/) - Go ORM library
