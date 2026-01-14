#!/bin/bash
set -e

echo "🧹 Cleaning up old containers..."
docker-compose -f example/docker-compose.yml down -v 2>/dev/null || true

echo ""
echo "🏗️  Building and starting services..."
docker-compose -f example/docker-compose.yml up -d --build

echo ""
echo "⏳ Waiting for services to be ready..."
sleep 15

echo ""
echo "📊 Service status:"
docker-compose -f example/docker-compose.yml ps

echo ""
echo "📝 Setup logs:"
docker-compose -f example/docker-compose.yml logs nis-setup

echo ""
echo "🗄️  Database tables:"
docker-compose -f example/docker-compose.yml exec -T postgres psql -U nis -d nis -c "\dt"

echo ""
echo "👤 Admin user:"
docker-compose -f example/docker-compose.yml exec -T postgres psql -U nis -d nis -c "SELECT id, username, role, created_at FROM api_users;"

echo ""
echo "🔍 Testing admin login..."
TOKEN=$(curl -s -X POST 'http://localhost:8080/nis.v1.AuthService/Login' -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}')
if echo "$TOKEN" | grep -q "token"; then
  echo "   ✓ Login successful!"
  echo "   User: $(echo "$TOKEN" | grep -o '"username":"[^"]*"')"
else
  echo "   ✗ Login failed!"
  echo "   $TOKEN"
fi

echo ""
echo "✅ PostgreSQL Docker Compose stack is up!"
echo "   UI: http://localhost:8080"
echo "   Login: admin / admin123"
echo "   PostgreSQL: localhost:5432 (user: nis, pass: nis_password, db: nis)"
