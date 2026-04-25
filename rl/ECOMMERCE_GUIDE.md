# 🛡️ Rate-Limited Ecommerce System

## Architecture Overview

This system demonstrates how a rate limiter protects backend services from overload using a distributed, atomic token bucket algorithm.

```
┌─────────────┐
│   Browser   │ (Customer/Client)
└──────┬──────┘
       │ HTTP (port 8080)
       ▼
┌──────────────────────────┐
│   Nginx (Load Balancer)  │
├──────────────────────────┤
│ ✓ Serves Frontend (/)    │
│ ✓ Routes /api/* → RateL. │
└──────┬───────────────────┘
       │ Round-robin across 3 instances
       ├─────────────┬─────────────┬──────────────┐
       ▼             ▼             ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ RateLimit #1 │ │ RateLimit #2 │ │ RateLimit #3 │
├──────────────┤ ├──────────────┤ ├──────────────┤
│ ✓ Check rate │ │ ✓ Check rate │ │ ✓ Check rate │
│ ✓ Proxy to   │ │ ✓ Proxy to   │ │ ✓ Proxy to   │
│   backend    │ │   backend    │ │   backend    │
└──────┬───────┘ └──────┬───────┘ └──────┬───────┘
       │                │                │
       └────────┬───────┴────────┬───────┘
                │                │
                ▼                ▼
         ┌─────────────────┐  ┌────────────────┐
         │  Backend:9000   │  │ Shared Redis   │
         ├─────────────────┤  ├────────────────┤
         │ ✓ Products      │  │ ✓ Token buckets│
         │ ✓ Cart Mgmt     │  │ ✓ Request logs │
         │ ✓ Orders        │  │ ✓ Distributed │
         │ ✓ Inventory     │  │   state        │
         └─────────────────┘  └────────────────┘

Monitoring:
┌──────────────────────────────┐
│ Prometheus (:9090)           │
│ ✓ Collects metrics from RL   │
│ ✓ Tracks requests/limits     │
└──────────────────────────────┘
         │
         ▼
┌──────────────────────────────┐
│ Grafana (:3000)              │
│ ✓ Rate limit dashboards      │
│ ✓ Backend performance        │
│ ✓ Real-time alerts           │
└──────────────────────────────┘
```

## Rate Limit Tiers for Ecommerce

Each tier protects against different overload scenarios:

### 1. **Global Tier** (10,000 req/min)
- **Purpose**: System-wide protection
- **Trigger**: Total traffic across all users
- **Scenario**: DDoS, system failure protection
- **Action**: If exceeded, everyone gets rate limited

### 2. **IP Tier** (60 req/min per IP)
- **Purpose**: Prevent single IP from overwhelming system
- **Trigger**: Requests from one IP address
- **Scenario**: Bot attacks, aggressive crawling
- **Action**: If exceeded, that IP gets 429 response

### 3. **User Tier** (200 req/min per user)
- **Purpose**: Fair usage per customer
- **Trigger**: Authenticated requests per user ID
- **Scenario**: User clicking too fast, buggy client
- **Action**: User must wait before next request

### 4. **API Key Tier** (500 req/min per API key)
- **Purpose**: Partner integrations quota
- **Trigger**: Requests with X-Api-Key header
- **Scenario**: Third-party API client using quota
- **Action**: API key client throttled

## Endpoint Protection Levels

| Endpoint | Tier | Limit | Use Case |
|----------|------|-------|----------|
| GET /api/products | IP, Global | 60/min | Read-heavy, low cost |
| GET /api/products/{id} | IP, Global | 60/min | Single product lookup |
| GET /api/cart | User | 200/min | User checking cart |
| POST /api/cart | User | 200/min | Adding to cart |
| DELETE /api/cart | User | 200/min | Clearing cart |
| **POST /api/checkout** | **User** | **200/min** | **Most critical** |
| GET /api/orders | User | 200/min | Order history |

**Key Protection:**
- ✅ Checkout is rate limited per user (prevents order bombing)
- ✅ All endpoints protected by IP tier (prevents single IP attack)
- ✅ Global tier prevents system-wide overload
- ✅ Burst guard blocks traffic spikes

## Running the System

### Start Everything
```bash
cd /Users/rajatshubraghoshal/Desktop/RSG/SystemDesign/RateLimiter/rl/docker

docker compose up -d
# Wait for services to start (~10 seconds)
docker compose ps
```

### Access the Application

| Service | URL | Purpose |
|---------|-----|---------|
| **Frontend** | http://localhost:8080 | Ecommerce store |
| **Prometheus** | http://localhost:9090 | Metrics (queries) |
| **Grafana** | http://localhost:3000 | Dashboards |
| **API Direct** | http://localhost:8080/api/* | Backend endpoints |
| **Rate Limiter Metrics** | http://localhost:8080/metrics | Rate limit stats |

### Frontend Usage

1. **Login**: Enter user ID (default: `user123`)
2. **Browse**: View products
3. **Shop**: Add items to cart
4. **Checkout**: Create order (rate limited!)
5. **Track**: View order history

### Testing Rate Limits

#### Test 1: Rapid Product Browsing
```bash
# Browse products rapidly - should mostly work (60/min per IP)
for i in {1..100}; do
  curl -s http://localhost:8080/api/products | jq '.count'
done

# After ~60 requests, you'll see 429 Too Many Requests
```

#### Test 2: Rapid Checkout (Most Important)
```bash
# Simulate spam checkout attempts per user
# Each user limited to 200 requests/min

curl -X POST http://localhost:8080/api/checkout \
  -H "X-User-ID: user123" \
  -H "Content-Type: application/json"

# Try multiple times rapidly - will be rate limited
```

#### Test 3: Different Users (Same IP)
```bash
# User 1: 200 requests available
curl http://localhost:8080/api/products \
  -H "X-User-ID: user-alice"

# User 2: Another 200 requests available
curl http://localhost:8080/api/products \
  -H "X-User-ID: user-bob"

# IP still limited to 60/min total - demonstrates tier hierarchy
```

#### Test 4: Burst Protection
```bash
# Rapid burst of requests - should be throttled after ~30 (burst limit)
for i in {1..50}; do
  curl -s -w "Status: %{http_code}\n" http://localhost:8080/api/products &
done
wait

# Demonstrates burst guard protection
```

## Monitoring in Grafana

### Dashboard 1: Real-Time Health
- ✓ Requests allowed/denied per second
- ✓ Denial rate percentage
- ✓ Error rate from Redis failures
- ✓ P99 latency to Redis

### Dashboard 2: Per-Tier Analysis
- ✓ Denials by rate limit tier
- ✓ Which tier causes most denials
- ✓ Token depletion per tier
- ✓ Burst violations

### Dashboard 3: Backend Protection
- ✓ Actual requests reaching backend
- ✓ Backend response times
- ✓ Failed requests (after rate limiting)
- ✓ System throughput

### Key Metrics to Watch

```promql
# Denial rate exceeding 5%
100 * sum(rate(ratelimiter_requests_denied_total[1m])) / 
    (sum(rate(ratelimiter_requests_allowed_total[1m])) + 
     sum(rate(ratelimiter_requests_denied_total[1m])))

# P99 latency to Redis
histogram_quantile(0.99, rate(ratelimiter_redis_latency_microseconds_bucket[1m]))

# Denials by tier (which is bottleneck?)
sum by(deny_tier)(rate(ratelimiter_requests_denied_total[1m]))

# Redis errors (fail-open path)
rate(ratelimiter_redis_errors_total[5m])
```

## How Rate Limiting Protects Backend

### Scenario 1: Bot Attack
- **Attack**: Bot sends 10,000 requests/sec from one IP
- **Protection**: 
  - ✅ IP tier blocks after ~1 request/sec
  - ✅ Backend never sees the attack
  - ✅ Other users unaffected (different IPs)

### Scenario 2: Broken Client Loop
- **Attack**: Mobile app has buggy infinite request loop
- **Protection**:
  - ✅ User tier limits to 200/min
  - ✅ App gets 429 responses
  - ✅ Client should retry with backoff
  - ✅ Backend survives

### Scenario 3: API Abuse (Checkout Spam)
- **Attack**: Attacker tries to place 1,000 fake orders/min
- **Protection**:
  - ✅ User tier: 200 checkout/min limit per user
  - ✅ Burst guard: max 100 orders in 60s window
  - ✅ Global tier: 10,000 orders/min total
  - ✅ Backend order processing stays responsive

### Scenario 4: Cascading Failure
- **Attack**: Inventory service slow → timeout → retry storm
- **Protection**:
  - ✅ Rate limiter cuts off traffic before backend breaks
  - ✅ Fail-open ensures degraded service, not total outage
  - ✅ Slow clients get queued by HTTP backpressure
  - ✅ System recovers when backend recovers

## Failure Scenarios & Recovery

### Redis Fails
- **Status**: Rate limiter goes to "fail-open" mode
- **Behavior**: Allows all requests (no rate limiting)
- **Detection**: Prometheus alerts when error rate > 1%
- **Recovery**: Restart Redis, rate limiting resumes

### Rate Limiter Container Dies
- **Nginx**: Routes to remaining 2 containers
- **Requests**: Some fail with 502, most route to healthy containers
- **Recovery**: Docker Compose restarts failed container

### Backend Overwhelmed
- **Rate Limiter**: Continues rate limiting (already happened!)
- **Client**: Gets 429 Retry-After header
- **Recovery**: Client backs off, backend recovers

### Network Partition
- **Rate Limiter**: Uses local Lua script (atomic)
- **Behavior**: Rate limiting still works
- **Recovery**: When network recovers, Redis catches up

## Configuration Tuning

### For High-Traffic Scenarios
```yaml
# Allow more requests but stricter burst control
global:
  capacity: 50_000      # 50k requests capacity
  refillRate: 20_000    # 20k req/sec refill
  burstLimit: 20_000    # Strict burst window

user:
  capacity: 1_000       # 1k per user
  refillRate: 500       # 500 req/sec
```

### For Strict Protection
```yaml
# Tight limits, prevent any abuse
ip:
  capacity: 10
  refillRate: 5         # 5 req/sec = 300/min
  burstLimit: 5         # Only 5 at once

user:
  capacity: 50
  refillRate: 25        # 25 req/sec
  burstLimit: 20        # No burst spikes
```

## Performance Characteristics

- **Per-request latency**: ~150-200µs (Redis round-trip)
- **Throughput capacity**: ~100k requests/sec (single Redis)
- **Memory per bucket**: ~500 bytes (token state + log)
- **Failure mode**: Fail-open (requests allowed on Redis failure)
- **Atomicity**: 100% guaranteed (Lua script execution)

## Scaling Considerations

### Horizontal Scaling
```
Current: 3 rate limiter containers → 100k req/sec capacity
Needed 1M req/sec? → Use Redis Cluster (sharded)
                   → Each shard handles 100k req/sec
                   → 10 shards = 1M req/sec
```

### Multi-Region
```
Region A → Redis A (rate limiter local)
Region B → Redis B (independent limits)
Region C → Redis C (isolated quotas)
```

## Security Best Practices

- ✅ Rate limiter runs on private network (not exposed)
- ✅ Only Nginx can access rate limiter
- ✅ Backend not directly exposed
- ✅ X-Real-IP header trusted only from Nginx
- ✅ Redis has no password (private network)
- ✅ Metrics endpoint secured (no sensitive data)

## Troubleshooting

### Requests getting 429 too often
```bash
# Check current rate limit state
curl 'http://localhost:8080/admin/limits?ip=YOUR_IP'

# Look at Prometheus metrics
# Is it the global tier or per-IP tier?
```

### High latency to Redis
```bash
# Check Redis connection pool
docker compose exec redis redis-cli info stats

# Check network latency
docker compose exec ratelimiter_1 \
  sh -c 'for i in {1..100}; do redis-cli --latency; done | tail -1'
```

### Uneven request distribution
```bash
# Check Nginx upstream status
docker compose exec nginx nginx -T | grep upstream -A 20

# Verify round-robin is working
for i in {1..30}; do
  curl -s http://localhost:8080/metrics | grep instance | head -1
done
```

## Key Takeaways

1. **Atomicity**: Single Redis operation = no race conditions
2. **Distributed**: Multiple containers = scale horizontally  
3. **Fail-Open**: Redis down = degraded service, not total outage
4. **Multi-Tier**: Different limits per IP/user/global
5. **Burst Guard**: Protection against traffic spikes
6. **Observable**: Full metrics + dashboards for monitoring

This architecture **protects the backend from overload** while maintaining availability and providing visibility into system health.
