package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// GCRATier defines rate limits using GCRA model.
type GCRATier struct {
	Label string

	// Requests per second (steady rate)
	Rate float64

	// Max burst allowed (in number of requests)
	Burst int

	// TTL for Redis key (should cover burst window)
	TTL time.Duration
}

// LimiterConfig is the top-level config for the service.
type LimiterConfig struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	Tiers []GCRATier

	// Headers for identity extraction
	IPHeader     string
	UserIDHeader string
	APIKeyHeader string

	// Mode: enforce | dry-run | bypass
	Mode string
}

func Default() LimiterConfig {
	return LimiterConfig{
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		IPHeader:     env("IP_HEADER", "X-Real-IP"),
		UserIDHeader: env("USER_ID_HEADER", "X-User-ID"),
		APIKeyHeader: env("API_KEY_HEADER", "X-Api-Key"),

		Tiers: []GCRATier{
			{
				Label: "global",
				Rate:  5000,
				Burst: 8000,
				TTL:   30 * time.Second,
			},
			{
				Label: "api_key",
				Rate:  100,
				Burst: 200,
				TTL:   60 * time.Second,
			},
			{
				Label: "user",
				Rate:  50,
				Burst: 100,
				TTL:   60 * time.Second,
			},
			{
				Label: "ip",
				Rate:  10,
				Burst: 30,
				TTL:   30 * time.Second,
			},
		},

		Mode: env("LIMITER_MODE", "enforce"), // enforce | dry-run | bypass
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func (c LimiterConfig) Validate() error {
	if c.RedisAddr == "" {
		return fmt.Errorf("RedisAddr is required")
	}
	if len(c.Tiers) == 0 {
		return fmt.Errorf("at least one tier is required")
	}

	for i, t := range c.Tiers {
		if t.Rate <= 0 {
			return fmt.Errorf("tier[%d] %q: Rate must be positive", i, t.Label)
		}
		if t.Burst <= 0 {
			return fmt.Errorf("tier[%d] %q: Burst must be positive", i, t.Label)
		}
		if t.TTL <= 0 {
			return fmt.Errorf("tier[%d] %q: TTL must be positive", i, t.Label)
		}
	}
	return nil
}
