#!/bin/bash
set -e

echo "=== NATS Identity Service - Example Setup ==="
echo ""

# Create data directories
echo "Creating data directories..."
mkdir -p ./data/nis ./data/nats/resolver ./data/nats/jetstream

# Start NIS
echo "Starting NIS server..."
docker-compose up -d nis
sleep 10

# Create admin user
echo "Creating admin user..."
docker exec example-nis ./nis user create admin --password admin123 --role admin

# Login with nisctl to get token
echo "Logging in with nisctl..."
TOKEN=$(docker exec example-nis ./nisctl login http://localhost:8080 --username admin --password admin123 --output json | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# Configure nisctl alias
echo "Setting up nisctl alias..."
nisctl() {
  docker exec example-nis ./nisctl --server http://localhost:8080 --token "$TOKEN" "$@"
}

# Create operator
echo "Creating operator..."
nisctl operator create demo-operator --description "Demo NATS operator"

# Generate NATS config
echo "Generating NATS configuration..."
nisctl operator generate-include demo-operator > ./data/nats/nats-server.conf

# Start NATS
echo "Starting NATS server with JWT auth..."
docker-compose up -d nats
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
echo "Test connection:"
echo "  nats --creds=./app-user.creds --server=nats://localhost:4222 rtt"
echo ""
echo "Use nisctl via docker exec:"
echo "  docker exec example-nis ./nisctl operator list"
echo ""
