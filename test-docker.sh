#!/bin/bash
set -e

echo "🧹 Cleaning up old containers..."
docker-compose down -v 2>/dev/null || true

echo ""
echo "🏗️  Building and starting services..."
docker-compose up -d --build

echo ""
echo "⏳ Waiting for services to be ready..."
sleep 10

echo ""
echo "📊 Service status:"
docker-compose ps

echo ""
echo "📝 Setup logs:"
docker-compose logs nis-setup

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
echo "✅ Docker Compose stack is up!"
echo "   UI: http://localhost:8080"
echo "   Login: admin / admin123"
