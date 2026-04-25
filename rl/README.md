# Distributed Rate Limiter — Go + Redis + Lua

A production-grade, horizontally scalable rate limiter that enforces **global request quotas consistently** across any number of stateless Go service instances.

---

## Architecture

```
Clients
  │
  ▼
[ Nginx — least-conn LB ]
  │           │           │
  ▼           ▼           ▼
[ Go instance 1 ]  [ Go instance 2 ]  [ Go instance 3 ]
  │                    │                    │
  └────────────────────┼────────────────────┘
                       │
              [ Redis — single source of truth ]
                   Lua atomic script
```

All Go instances are **completely stateless** — every rate limit decision is made by a single atomic Lua script execution in Redis.  
No instance keeps any local state. Scaling out adds capacity, not inconsistency.

---

## Algorithm: Token Bucket + Sliding Window Burst Guard

Each request is checked against **two independent guards**:

### 1. Token Bucket (refill-rate limiting)
- Each identity (IP / user / API key) has a bucket with `capacity` tokens.
- Tokens refill at `refill_rate` per second (continuous, not step-based).
- A request consumes `requested` tokens (default: 1).
- If the bucket has fewer tokens than requested → **deny**, compute `retry_after`.

```
new_tokens = min(capacity, stored_tokens + elapsed_seconds × refill_rate)
allowed    = new_tokens >= requested
```

### 2. Sliding Window Burst Guard
- Independently tracks how many requests have occurred in the last `window_size`.
- If `count >= burst_limit` → **deny** even if the token bucket allows it.
- Prevents request storms that fit within the token budget but are too spiky.

**Both checks run in a single Lua script** — one Redis round-trip, fully atomic.

---

## Why Lua in Redis?

| Approach | Atomicity | Round-trips | Complexity |
|---|---|---|---|
| GET + SET in Go | ❌ race condition | 2 | Low |
| WATCH / MULTI / EXEC | ⚠ retry loops | 3–5 | Medium |
| Redis Lua script | ✅ atomic | **1** | Low |

The Lua script runs inside Redis as a single command. No other client can observe or mutate the key between the read and write. This is the standard production pattern used by Stripe, GitHub, and others.

The script SHA is pre-loaded on startup (`EVALSHA`) — subsequent calls skip script transmission entirely.

---

## Multi-Tier Quota Model

Requests are checked against multiple tiers **in order**. The first denial wins.

| Tier | Key | Default capacity | Default rate |
|---|---|---|---|
| `global` | `rl:global` | 10,000 | 5,000 req/s |
| `api_key` | `rl:apikey:<key>` | 500 | 100 req/s |
| `user` | `rl:user:<id>` | 200 | 50 req/s |
| `ip` | `rl:ip:<addr>` | 60 | 10 req/s |

Anonymous requests (no user ID, no API key) are only subject to the global and IP tiers. Authenticated requests get more generous quotas.

---

## Response Headers

Every response carries rate limit headers (RFC draft compliance):

```
X-RateLimit-Remaining:     42
X-RateLimit-Reset:         1712345678
Retry-After:               3          # seconds, only on 429
X-RateLimit-RetryAfter-Ms: 2847       # milliseconds, only on 429
```

---

## Fail-Open Design

If Redis is unreachable, the limiter **allows the request** and increments `ratelimiter_redis_errors_total`. This means:

- A Redis outage degrades to "no rate limiting" rather than "total service outage".
- Prometheus alerts on the error counter trigger on-call response.
- You can flip this to fail-closed per tier by changing the error handler in `limiter.go`.

---

## Project Structure

```
.
├── cmd/server/main.go              — HTTP server entrypoint
├── internal/
│   ├── config/config.go            — TierConfig, LimiterConfig, defaults
│   ├── limiter/
│   │   ├── limiter.go              — Redis client, Lua invocation, Decision
│   │   └── limiter_test.go         — unit + concurrency + benchmark tests
│   ├── middleware/ratelimit.go     — HTTP middleware, header writing, IP extraction
│   └── metrics/metrics.go         — Prometheus counters, histograms, gauges
├── scripts/token_bucket.lua        — Atomic Lua script (embedded via go:embed)
├── docker/
│   ├── Dockerfile                  — Multi-stage, scratch final image (~6 MB)
│   ├── docker-compose.yml          — Redis + 3× Go + Nginx + Prometheus + Grafana
│   ├── nginx.conf                  — Least-conn upstream config
│   ├── prometheus.yml              — Scrape config for all replicas
│   └── grafana/                    — Auto-provisioned dashboard
└── load_test.sh                    — Burst test (curl) + sustained test (vegeta)
```

---

## Quickstart

```bash
# Start everything
cd docker
docker compose up --build

# Hit the API
curl http://localhost/api/data

# Hammer it to trigger rate limiting
TOTAL=200 ./load_test.sh

# Sustained 500 req/s load test (requires vegeta)
RATE=500 DURATION=60s ./load_test.sh --vegeta

# Grafana dashboard
open http://localhost:3000

# Prometheus
open http://localhost:9090
```

---

## Standalone Docker Gateway

This rate limiter is designed to run as a generic API Gateway or sidecar container in front of **any** backend application.

### Building the Image
```bash
docker build -t ratelimiter -f docker/Dockerfile .
```

### Running the Gateway
Simply set the `BACKEND_URL` environment variable to point to your target application. All traffic hitting the rate limiter will be transparently proxied and rate-limited.

```bash
docker run -p 8080:8080 \
  -e REDIS_ADDR=redis:6379 \
  -e BACKEND_URL=http://your-app:3000 \
  -e LISTEN_ADDR=:8080 \
  ratelimiter
```

---

## Configuration

All config is in `internal/config/config.go`. Override via environment variables:

| Env var | Default | Description |
|---|---|---|
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | `` | Redis AUTH password |
| `REDIS_DB` | `0` | Redis DB index |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `IP_HEADER` | `X-Real-IP` | Header for client IP (behind proxy) |
| `USER_ID_HEADER` | `X-User-ID` | Header for authenticated user ID |
| `API_KEY_HEADER` | `X-Api-Key` | Header for API key |

---

## Testing

```bash
# All tests
go test ./...

# Race detector
go test -race ./...

# Benchmarks
go test -bench=. -benchmem ./internal/limiter/

# Specific test
go test -run TestConcurrentRequestsDoNotExceedCapacity ./internal/limiter/
```

The test suite uses **miniredis** — an in-process Redis implementation that runs the Lua scripts faithfully. No Redis daemon needed for tests.

---

## Scaling & Operations

**Horizontal scaling**: Add Go instances freely. Each instance shares the same Redis, so global quotas are enforced identically regardless of which instance handles a request.

**Redis scaling**: For very high throughput (>100k req/s), use Redis Cluster and shard by tenant ID prefix. The Lua script works identically in cluster mode as long as both keys (`rl:<tier>:<id>` and `rl:log:<tier>:<id>`) hash to the same slot — use Redis hash tags: `{rl:ip:1.2.3.4}` and `{rl:log:ip:1.2.3.4}`.

**Redis persistence**: Not needed. Rate limit state is inherently ephemeral. Disable RDB/AOF for maximum performance.

**Memory**: Each active limiter key uses ~80 bytes. 1 million active IPs ≈ 80 MB.

---

## Prometheus Alerts (example)

```yaml
- alert: HighDenialRate
  expr: >
    sum(rate(ratelimiter_requests_denied_total[5m])) /
    sum(rate(ratelimiter_requests_allowed_total[5m])) > 0.1
  for: 2m
  annotations:
    summary: "More than 10% of requests are being rate-limited"

- alert: RedisFailOpen
  expr: increase(ratelimiter_redis_errors_total[5m]) > 0
  annotations:
    summary: "Rate limiter failing open — Redis unreachable"

- alert: HighRedisLatency
  expr: histogram_quantile(0.99, rate(ratelimiter_redis_latency_microseconds_bucket[1m])) > 1000
  annotations:
    summary: "Rate limiter p99 latency > 1ms"
```
