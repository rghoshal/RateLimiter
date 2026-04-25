#!/usr/bin/env bash
# Rate-Limited Ecommerce System - Quick Reference & Operations Guide

# ────────────────────────────────────────────────────────────────────────────────
# 🚀 STARTUP & SHUTDOWN
# ────────────────────────────────────────────────────────────────────────────────

# Start everything
cd docker && docker compose up -d && cd ..

# Stop everything
cd docker && docker compose down && cd ..

# View running containers
docker compose ps

# View logs for a service
docker compose logs -f ratelimiter_1
docker compose logs -f backend
docker compose logs -f nginx

# Rebuild containers
docker compose build --no-cache

# ────────────────────────────────────────────────────────────────────────────────
# 🌐 ACCESS SERVICES
# ────────────────────────────────────────────────────────────────────────────────

# Frontend
open http://localhost:8080

# Prometheus (metrics database)
open http://localhost:9090

# Grafana (dashboards)
open http://localhost:3000

# ────────────────────────────────────────────────────────────────────────────────
# 🧪 TESTING RATE LIMITS
# ────────────────────────────────────────────────────────────────────────────────

# Test 1: Check if services are healthy
curl http://localhost:8080/health
echo ""

# Test 2: Browse products (should work, 60/min per IP)
curl -s http://localhost:8080/api/products | jq '.count'

# Test 3: Rapid requests - see rate limiting in action
echo "Sending 100 requests rapidly..."
for i in {1..100}; do
  RESPONSE=$(curl -s -w "%{http_code}" http://localhost:8080/api/products)
  HTTP_CODE="${RESPONSE: -3}"
  if [ "$HTTP_CODE" != "200" ]; then
    echo "Request $i: HTTP $HTTP_CODE (Rate Limited!)"
  fi
done

# Test 4: Different users (different quotas)
echo "User Alice:"
curl -s -H "X-User-ID: alice" http://localhost:8080/api/cart | jq '.'

echo "User Bob:"
curl -s -H "X-User-ID: bob" http://localhost:8080/api/cart | jq '.'

# Test 5: Check current rate limit state
curl -s http://localhost:8080/admin/limits?ip=127.0.0.1 | jq '.'

# Test 6: Add to cart
curl -X POST http://localhost:8080/api/cart \
  -H "X-User-ID: user123" \
  -H "Content-Type: application/json" \
  -d '{"product_id": 1, "quantity": 1}'

# Test 7: View cart
curl -H "X-User-ID: user123" http://localhost:8080/api/cart

# Test 8: Checkout
curl -X POST http://localhost:8080/api/checkout \
  -H "X-User-ID: user123" \
  -H "Content-Type: application/json"

# Test 9: View orders
curl -H "X-User-ID: user123" http://localhost:8080/api/orders

# ────────────────────────────────────────────────────────────────────────────────
# 📊 MONITORING & METRICS
# ────────────────────────────────────────────────────────────────────────────────

# View rate limiter metrics (Prometheus format)
curl -s http://localhost:8080/metrics | grep ratelimiter | head -20

# Query Prometheus directly
# Requests per second
curl 'http://localhost:9090/api/v1/query?query=sum(rate(ratelimiter_requests_allowed_total[1m]))'

# Denial rate
curl 'http://localhost:9090/api/v1/query?query=sum(rate(ratelimiter_requests_denied_total[1m]))'

# Denials by tier
curl 'http://localhost:9090/api/v1/query?query=sum%20by(deny_tier)(rate(ratelimiter_requests_denied_total[1m]))'

# Redis latency p99
curl 'http://localhost:9090/api/v1/query?query=histogram_quantile(0.99,rate(ratelimiter_redis_latency_microseconds_bucket[1m]))'

# ────────────────────────────────────────────────────────────────────────────────
# 🔍 DEBUGGING
# ────────────────────────────────────────────────────────────────────────────────

# Check Redis connection
docker compose exec redis redis-cli ping

# View Redis keys (all rate limit buckets)
docker compose exec redis redis-cli KEYS 'rl:*' | head -20

# Get token bucket state for an IP
docker compose exec redis redis-cli HGETALL 'rl:ip:127.0.0.1'

# Get request log (sliding window)
docker compose exec redis redis-cli ZRANGE 'rl:log:ip:127.0.0.1' 0 -1 WITHSCORES

# Check Redis memory usage
docker compose exec redis redis-cli INFO memory

# Monitor Redis commands in real-time
docker compose exec redis redis-cli MONITOR

# ────────────────────────────────────────────────────────────────────────────────
# 📈 LOAD TESTING
# ────────────────────────────────────────────────────────────────────────────────

# Sustained load (100 req/sec for 30 seconds)
# Note: Adjust concurrency based on your system
for i in {1..3000}; do
  curl -s http://localhost:8080/api/products &
  if (( i % 100 == 0 )); then echo "Sent $i requests"; fi
  if (( i % 10 == 0 )); then sleep 0.1; fi
done
wait

# Burst test (simulate traffic spike)
echo "Burst test: 500 concurrent requests..."
for i in {1..500}; do
  curl -s http://localhost:8080/api/products > /dev/null &
done
wait
echo "Done!"

# Sustained checkout spam (test user tier limit)
echo "Checkout spam test (should hit 200/min limit)..."
for i in {1..300}; do
  curl -s -X POST http://localhost:8080/api/checkout \
    -H "X-User-ID: spammer" \
    -H "Content-Type: application/json" &
  if (( i % 50 == 0 )); then echo "Sent $i checkout requests"; fi
done
wait

# ────────────────────────────────────────────────────────────────────────────────
# 🛠️ OPERATIONAL TASKS
# ────────────────────────────────────────────────────────────────────────────────

# Restart a specific service
docker compose restart ratelimiter_1
docker compose restart backend

# Force recreate containers
docker compose up -d --force-recreate

# View resource usage
docker stats

# Export metrics for analysis
curl -s http://localhost:8080/metrics > metrics_export.txt

# Clear Redis cache (WARNING: clears all rate limit state!)
docker compose exec redis redis-cli FLUSHDB

# Scale the backend (docker compose feature)
# To add more backends, modify docker-compose.yml and restart

# ────────────────────────────────────────────────────────────────────────────────
# 🐛 TROUBLESHOOTING
# ────────────────────────────────────────────────────────────────────────────────

# Check if services are responsive
for svc in redis ratelimiter_1 backend; do
  echo "Checking $svc..."
  docker compose exec $svc sh -c "echo OK" && echo "$svc: ✓ OK" || echo "$svc: ✗ FAILED"
done

# Test rate limiter can reach Redis
docker compose exec ratelimiter_1 \
  sh -c 'redis-cli -h redis ping'

# Test rate limiter can reach backend
docker compose exec ratelimiter_1 \
  sh -c 'curl -s http://backend:9000/health | jq .'

# Check network connectivity
docker compose exec ratelimiter_1 \
  sh -c 'apk add ping && ping -c 1 redis'

# View rate limiter server logs
docker compose logs --tail 50 ratelimiter_1

# Check if ports are listening
netstat -tlnp 2>/dev/null | grep -E ':(8080|9090|3000|6379|9000)'
# Or on macOS:
lsof -i -P -n | grep LISTEN

# ────────────────────────────────────────────────────────────────────────────────
# 📚 COMMON COMMANDS
# ────────────────────────────────────────────────────────────────────────────────

# Get all available Prometheus metrics
curl -s http://localhost:9090/api/v1/label/__name__/values | jq '.data | map(select(. | startswith("ratelimiter")))' | head -20

# Get rate limit history (past 5 minutes)
curl 'http://localhost:9090/api/v1/query_range?query=rate(ratelimiter_requests_denied_total[1m])&start=now-5m&end=now&step=1m' | jq '.'

# Export Grafana dashboard
curl -s http://localhost:3000/api/dashboards/uid/ratelimiter-v1 | jq '.'

# ────────────────────────────────────────────────────────────────────────────────
# ⚡ PERFORMANCE BENCHMARKS
# ────────────────────────────────────────────────────────────────────────────────

# Measure average latency
echo "Measuring latency (10 requests)..."
for i in {1..10}; do
  curl -s -w "Request $i: %{time_total}s\n" http://localhost:8080/api/products > /dev/null
done

# Measure throughput
echo "Measuring throughput (1000 requests)..."
START=$(date +%s%N | cut -b1-13)
for i in {1..1000}; do
  curl -s http://localhost:8080/api/products > /dev/null &
done
wait
END=$(date +%s%N | cut -b1-13)
TIME_MS=$((END - START))
RPS=$((1000000 / (TIME_MS/1000)))
echo "Completed 1000 requests in ${TIME_MS}ms = $RPS req/sec"

# ────────────────────────────────────────────────────────────────────────────────
# 🎯 RATE LIMIT TIERS REFERENCE
# ────────────────────────────────────────────────────────────────────────────────

# Global Tier: 10,000 req/min (system-wide)
#  └─ If exceeded: Everyone gets rate limited

# IP Tier: 60 req/min per IP address
#  └─ If exceeded: That IP address blocked

# User Tier: 200 req/min per user (X-User-ID header)
#  └─ If exceeded: That user blocked

# API Key Tier: 500 req/min per API key (X-Api-Key header)
#  └─ If exceeded: That API key blocked

# Burst Guard: 30 requests in 60-second window (per IP)
#  └─ If exceeded: Request blocked even if tokens available

# ────────────────────────────────────────────────────────────────────────────────

echo "✨ Quick Reference Guide Loaded!"
echo "Run any of the above commands to test the system."
