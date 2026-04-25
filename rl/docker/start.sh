#!/bin/bash

# Quick start script for rate-limited ecommerce system

set -e

echo "🚀 Starting Rate-Limited Ecommerce System..."
echo ""

cd "$(dirname "$0")/docker"

echo "📦 Building containers..."
docker compose build

echo ""
echo "🐳 Starting services..."
docker compose up -d

echo ""
echo "⏳ Waiting for services to be ready..."
sleep 10

echo ""
echo "✅ Services started! Here's what's running:"
docker compose ps

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🌐 FRONTEND & API GATEWAY (Protected by Rate Limiter)"
echo "   URL: http://localhost:8080"
echo "   Features:"
echo "   - Browse products"
echo "   - Manage shopping cart"
echo "   - Place orders (rate limited!)"
echo "   - View order history"
echo ""
echo "📊 MONITORING & METRICS"
echo "   Prometheus: http://localhost:9090"
echo "   Grafana:    http://localhost:3000"
echo ""
echo "🛠️  DIRECT API ACCESS (for testing)"
echo "   GET  /api/products                 - List all products"
echo "   GET  /api/products/{id}            - Get product details"
echo "   GET  /api/cart                     - View user's cart"
echo "   POST /api/cart                     - Add item to cart"
echo "   DELETE /api/cart                   - Clear cart"
echo "   POST /api/checkout                 - Place order (⚠️ RATE LIMITED)"
echo "   GET  /api/orders                   - View user's orders"
echo ""
echo "📋 TESTING RATE LIMITS"
echo ""
echo "   Test rapid browsing (60/min per IP):"
echo "   $ for i in {1..100}; do curl http://localhost:8080/api/products; done"
echo ""
echo "   Test checkout limits (200/min per user):"
echo "   $ curl -X POST http://localhost:8080/api/checkout \\"
echo "       -H 'X-User-ID: user123' \\"
echo "       -H 'Content-Type: application/json'"
echo ""
echo "   Multiple users (same IP, different quotas):"
echo "   $ curl -H 'X-User-ID: alice' http://localhost:8080/api/products"
echo "   $ curl -H 'X-User-ID: bob' http://localhost:8080/api/products"
echo ""
echo "📚 DOCUMENTATION"
echo "   See: ECOMMERCE_GUIDE.md for complete documentation"
echo ""
echo "🛑 TO STOP ALL SERVICES"
echo "   $ docker compose down"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "✨ System is ready! Start shopping with rate limit protection! 🛡️"
echo ""
