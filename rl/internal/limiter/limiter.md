// Package limiter provides a distributed, multi-tier rate limiter backed
// by Redis. Each limit check is a single atomic Lua script execution —
// no multi-step transactions, no WATCH/MULTI/EXEC races.
package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/ratelimiter/internal/config"
)

//go:embed token_bucket.lua
var tokenBucketScript string

// Result is the outcome of a single-tier check.
type Result struct {
	Tier           string
	Allowed        bool
	TokensLeft     int64
	RetryAfterMs   int64
	BurstRemaining int64
	ResetAtMs      int64
}

// Decision aggregates all tier results.
type Decision struct {
	Allowed   bool
	DenyTier  string   // first tier that denied, if any
	Results   []Result // one per evaluated tier
	LatencyUs int64    // total Redis round-trip µs
}

// Limiter is safe for concurrent use by multiple goroutines.
type Limiter struct {
	rdb    redis.UniversalClient
	script *redis.Script
	cfg    config.LimiterConfig
	rng    *rand.Rand
}

// New creates a Limiter and pre-loads the Lua script SHA into Redis.
func New(cfg config.LimiterConfig) (*Limiter, error) {
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        []string{cfg.RedisAddr},
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		PoolSize:     64,
		MinIdleConns: 8,
		// Sub-millisecond timeouts — fail fast under Redis pressure
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  150 * time.Millisecond,
		WriteTimeout: 150 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	script := redis.NewScript(tokenBucketScript)
	// Pre-load script SHA — subsequent calls use EVALSHA (faster, less traffic)
	if err := script.Load(ctx, rdb).Err(); err != nil {
		return nil, fmt.Errorf("lua script load: %w", err)
	}

	return &Limiter{
		rdb:    rdb,
		script: script,
		cfg:    cfg,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec
	}, nil
}

// Allow checks all applicable tiers for the given identity trio.
// Identifiers that are empty strings skip their respective tiers.
func (l *Limiter) Allow(ctx context.Context, ip, userID, apiKey string) (*Decision, error) {
	start := time.Now()

	// Build (key, tier) pairs to evaluate
	type check struct {
		bucketKey string
		logKey    string
		tier      config.TierConfig
	}

	checks := make([]check, 0, 4)

	// Global tier — always evaluated
	for _, t := range l.cfg.Tiers {
		var bucketKey, logKey string
		switch t.Label {
		case "global":
			bucketKey = "rl:global"
			logKey = "rl:log:global"
		case "ip":
			if ip == "" {
				continue
			}
			bucketKey = "rl:ip:" + ip
			logKey = "rl:log:ip:" + ip
		case "user":
			if userID == "" {
				continue
			}
			bucketKey = "rl:user:" + userID
			logKey = "rl:log:user:" + userID
		case "api_key":
			if apiKey == "" {
				continue
			}
			bucketKey = "rl:apikey:" + apiKey
			logKey = "rl:log:apikey:" + apiKey
		default:
			// Custom tier: skip if no matching identifier
			continue
		}
		checks = append(checks, check{bucketKey, logKey, t})
	}

	nowMs := time.Now().UnixMilli()
	results := make([]Result, 0, len(checks))
	decision := &Decision{Allowed: true}

	// Execute all checks. In the deny case we still run remaining tiers
	// so callers see the most restrictive Retry-After across all tiers.
	for _, c := range checks {
		t := c.tier
		ttlSec := int64(t.TTL.Seconds())
		if ttlSec <= 0 {
			ttlSec = 300
		}

		res, err := l.script.Run(ctx, l.rdb,
			[]string{c.bucketKey, c.logKey},
			strconv.FormatFloat(t.Capacity, 'f', -1, 64),
			strconv.FormatFloat(t.RefillRate, 'f', -1, 64),
			strconv.FormatFloat(t.Requested, 'f', -1, 64),
			strconv.FormatInt(nowMs, 10),
			strconv.FormatInt(t.WindowSize.Milliseconds(), 10),
			strconv.Itoa(t.BurstLimit),
			strconv.FormatInt(ttlSec, 10),
		).Int64Slice()

		if err != nil {
			// On Redis failure, fail open (allow) to avoid cascading outage.
			// Metrics will capture the error; alerting handles it.
			log.Printf("Rate limiter Redis error for tier %s (bucket: %s): %v - failing open", t.Label, c.bucketKey, err)
			results = append(results, Result{
				Tier:    t.Label,
				Allowed: true, // fail-open
			})
			continue
		}

		r := Result{
			Tier:           t.Label,
			Allowed:        res[0] == 1,
			TokensLeft:     res[1],
			RetryAfterMs:   res[2],
			BurstRemaining: res[3],
			ResetAtMs:      res[4],
		}
		results = append(results, r)

		if !r.Allowed && decision.Allowed {
			decision.Allowed = false
			decision.DenyTier = t.Label
			log.Printf("Rate limit exceeded - denied by tier %s (bucket: %s, ip: %s, user: %s, api_key: %s, tokens_left: %d, retry_after: %dms)",
				t.Label, c.bucketKey, ip, userID, apiKey, r.TokensLeft, r.RetryAfterMs)
		}
	}

	decision.Results = results
	decision.LatencyUs = time.Since(start).Microseconds()

	// Log final decision
	if !decision.Allowed {
		log.Printf("Request DENIED by tier %s (ip: %s, user: %s, api_key: %s, latency: %dµs)",
			decision.DenyTier, ip, userID, apiKey, decision.LatencyUs)
	} else {
		log.Printf("Request ALLOWED (ip: %s, user: %s, api_key: %s, latency: %dµs)",
			ip, userID, apiKey, decision.LatencyUs)
	}

	return decision, nil
}

// Close shuts down the Redis connection pool.
func (l *Limiter) Close() error {
	return l.rdb.Close()
}

// MaxRetryAfterMs returns the highest retry-after value across all tier results.
func (d *Decision) MaxRetryAfterMs() int64 {
	var max int64
	for _, r := range d.Results {
		if !r.Allowed && r.RetryAfterMs > max {
			max = r.RetryAfterMs
		}
	}
	return max
}
