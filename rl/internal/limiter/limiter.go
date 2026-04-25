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

//go:embed gcra.lua
var gcraScript string

type Result struct {
	Tier         string
	Allowed      bool
	RetryAfterMs int64
	Remaining    int64
	ResetAtMs    int64
}

type Decision struct {
	Allowed   bool
	DenyTier  string
	Results   []Result
	LatencyUs int64
}

type Limiter struct {
	rdb    redis.UniversalClient
	script *redis.Script
	cfg    config.LimiterConfig
	rng    *rand.Rand
}

func New(cfg config.LimiterConfig) (*Limiter, error) {
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        []string{cfg.RedisAddr},
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		PoolSize:     64,
		MinIdleConns: 8,
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  150 * time.Millisecond,
		WriteTimeout: 150 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	script := redis.NewScript(gcraScript)
	if err := script.Load(ctx, rdb).Err(); err != nil {
		return nil, fmt.Errorf("lua script load: %w", err)
	}

	return &Limiter{
		rdb:    rdb,
		script: script,
		cfg:    cfg,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

func (l *Limiter) Allow(ctx context.Context, ip, userID, apiKey string) (*Decision, error) {
	start := time.Now()
	decision := &Decision{Allowed: true}

	if l.cfg.Mode == "bypass" {
		return decision, nil
	}

	// ─────────────────────────────────────────────
	// Build KEYS + ARGV (multi-tier)
	// ─────────────────────────────────────────────
	keys := make([]string, 0, len(l.cfg.Tiers))
	args := make([]interface{}, 0, len(l.cfg.Tiers)*3)

	for _, t := range l.cfg.Tiers {
		var key string

		switch t.Label {
		case "global":
			key = "rl:global"

		case "ip":
			if ip == "" {
				continue
			}
			key = "rl:ip:" + ip

		case "user":
			if userID == "" {
				continue
			}
			key = "rl:user:" + userID

		case "api_key":
			if apiKey == "" {
				continue
			}
			key = "rl:apikey:" + apiKey

		default:
			continue
		}

		// safety check
		if t.Rate <= 0 || t.Burst <= 0 {
			return nil, fmt.Errorf("invalid tier config: %s", t.Label)
		}

		burstWindowMs := int64(float64(t.Burst) / t.Rate * 1000)
		ttlMs := maxInt64(t.TTL.Milliseconds(), burstWindowMs*2)

		keys = append(keys, key)

		args = append(args,
			strconv.FormatFloat(t.Rate, 'f', -1, 64),
			strconv.Itoa(t.Burst),
			strconv.FormatInt(ttlMs, 10),
		)
	}

	// ─────────────────────────────────────────────
	// Redis call (single atomic execution)
	// ─────────────────────────────────────────────
	timeout := 50 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}

	ctxCall, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := l.script.Run(ctxCall, l.rdb, keys, args...).Int64Slice()
	if err != nil {
		log.Printf("Redis error (multi-tier): %v - fail open", err)

		return &Decision{
			Allowed:   true,
			LatencyUs: time.Since(start).Microseconds(),
		}, nil
	}

	// ─────────────────────────────────────────────
	// Map response
	// ─────────────────────────────────────────────
	decision.Allowed = res[0] == 1
	decision.LatencyUs = time.Since(start).Microseconds()

	decision.Results = []Result{
		{
			Tier:         "combined",
			Allowed:      decision.Allowed,
			RetryAfterMs: res[1],
			Remaining:    res[2],
			ResetAtMs:    res[3],
		},
	}

	if !decision.Allowed {
		decision.DenyTier = "multi-tier"

		log.Printf("Request DENIED (ip: %s, user: %s, api_key: %s, latency: %dµs)",
			ip, userID, apiKey, decision.LatencyUs)
	}

	if l.cfg.Mode == "dry-run" {
		decision.Allowed = true
	}

	if l.rng.Intn(1000) == 0 {
		log.Printf("sample ALLOWED ...")
	}

	return decision, nil
}

func (l *Limiter) Close() error {
	return l.rdb.Close()
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
