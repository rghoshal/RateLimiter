package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// TierConfig defines rate limits for one quota tier.
type TierConfig struct {
	// Bucket parameters
	Capacity   float64 // max tokens (burst ceiling)
	RefillRate float64 // tokens refilled per second
	Requested  float64 // tokens consumed per request (default 1)

	// Sliding-window burst guard
	WindowSize time.Duration // observation window
	BurstLimit int           // max requests in window

	// Redis TTL (should be > WindowSize)
	TTL time.Duration

	// Human label for metrics/logs
	Label string
}

// LimiterConfig is the top-level config for the service.
type LimiterConfig struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Ordered list of tiers evaluated per request.
	// All tiers that match are checked; first denial wins.
	Tiers []TierConfig

	// Header names used to extract identifiers
	IPHeader     string // e.g. "X-Real-IP"  (fallback: RemoteAddr)
	UserIDHeader string // e.g. "X-User-ID"
	APIKeyHeader string // e.g. "X-Api-Key"

	// Global instance limit (applied regardless of tier)
	GlobalCapacity  float64
	GlobalRate      float64
	GlobalBurst     int
	GlobalWindowSec int
	// Global switches
	Mode string // if true , everything allowed
}

// Default returns a sane production-ready default config.
func Default() LimiterConfig {
	return LimiterConfig{
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		IPHeader:     env("IP_HEADER", "X-Real-IP"),
		UserIDHeader: env("USER_ID_HEADER", "X-User-ID"),
		APIKeyHeader: env("API_KEY_HEADER", "X-Api-Key"),

		Tiers: []TierConfig{
			{
				Label:      "global",
				Capacity:   10_000,
				RefillRate: 5_000,
				Requested:  1,
				WindowSize: 10 * time.Second,
				BurstLimit: 8_000,
				TTL:        60 * time.Second,
			},
			{
				Label:      "api_key",
				Capacity:   500,
				RefillRate: 100,
				Requested:  1,
				WindowSize: 60 * time.Second,
				BurstLimit: 200,
				TTL:        5 * time.Minute,
			},
			{
				Label:      "user",
				Capacity:   200,
				RefillRate: 50,
				Requested:  1,
				WindowSize: 60 * time.Second,
				BurstLimit: 100,
				TTL:        5 * time.Minute,
			},
			{
				Label:      "ip",
				Capacity:   60,
				RefillRate: 10,
				Requested:  1,
				WindowSize: 60 * time.Second,
				BurstLimit: 30,
				TTL:        5 * time.Minute,
			},
		},
		Mode: env("MODE", "enforce"), // enforce | dry-run | bypass
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
		if t.Capacity <= 0 || t.RefillRate <= 0 {
			return fmt.Errorf("tier[%d] %q: Capacity and RefillRate must be positive", i, t.Label)
		}
	}
	return nil
}
