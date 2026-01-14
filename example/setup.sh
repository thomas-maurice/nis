#!/bin/bash
set -e

echo "=== NATS Identity Service - Example Setup ==="
echo ""

# Create data directories
echo "Creating data directories..."
mkdir -p ./data/nis ./data/nats/resolver ./data/nats/jetstream

# Create initial NATS config (will be overwritten after operator creation)
cat > ./data/nats/nats-server.conf << 'EOF'
port: 4222
http_port: 8222
jetstream {
  store_dir: /data/jetstream
}
EOF

# Start NIS
echo "Starting NIS server..."
docker-compose up -d nis
sleep 10

# Create admin user (may already exist from nis-setup container)
echo "Creating admin user..."
docker exec nis-server ./nis user create admin --password admin123 --role admin 2>/dev/null || echo "Admin user already exists"

# Login with nisctl to get token
echo "Logging in with nisctl..."
TOKEN=$(docker exec nis-server ./nisctl login http://localhost:8080 --username admin --password admin123 --output json | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# Configure nisctl alias
echo "Setting up nisctl alias..."
nisctl() {
  docker exec nis-server ./nisctl --server http://localhost:8080 --token "$TOKEN" "$@"
}

# Create operator
echo "Creating operator..."
nisctl operator create demo-operator --description "Demo NATS operator"

# Generate NATS config
echo "Generating NATS configuration..."
nisctl operator generate-include demo-operator > ./data/nats/nats-server.conf

# Start NATS and restart to load JWT auth config
echo "Starting NATS server with JWT auth..."
docker-compose up -d nats
sleep 3
docker-compose restart nats
sleep 5

# Restart NIS so it can resolve the nats hostname
echo "Restarting NIS server to refresh DNS..."
docker-compose restart nis
sleep 5

# Create cluster
echo "Registering NATS cluster..."
nisctl cluster create demo-cluster \
  --operator demo-operator \
  --urls nats://nats:4222 \
  --description "Demo cluster"

# Create account
echo "Creating test account..."
nisctl account create app-account \
  --operator demo-operator \
  --description "Application account" \
  --max-memory 1073741824 \
  --max-storage 10737418240 \
  --max-streams 10 \
  --max-consumers 100

# Create user
echo "Creating test user..."
nisctl user create app-user \
  --operator demo-operator \
  --account app-account \
  --description "Application user"

# Sync to cluster
echo "Syncing accounts to NATS..."
nisctl cluster sync demo-cluster

# Get credentials
echo "Generating user credentials..."
nisctl user creds app-user \
  --operator demo-operator \
  --account app-account \
  > app-user.creds

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ Setup complete!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Web UI:    http://localhost:8080"
echo "Login:     admin / admin123"
echo "NATS:      nats://localhost:4222"
echo "Creds:     ./app-user.creds"
echo ""
echo "Test connection with Go:"
cat << 'GOTEST'
  go run -mod=mod - <<'EOF'
  package main
  import (
    "fmt"
    "github.com/nats-io/nats.go"
  )
  func main() {
    nc, err := nats.Connect("nats://localhost:4222", nats.UserCredentials("./app-user.creds"))
    if err != nil { panic(err) }
    defer nc.Close()
    fmt.Println("Connected to NATS!")
  }
  EOF
GOTEST
echo ""
echo "Use nisctl via docker exec:"
echo "  docker exec nis-server ./nisctl operator list"
echo ""
