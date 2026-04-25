// Package middleware provides HTTP handler wrappers for rate limiting.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/example/ratelimiter/internal/config"
	"github.com/example/ratelimiter/internal/limiter"
	"github.com/example/ratelimiter/internal/metrics"
)

// RateLimit returns a middleware that enforces distributed rate limits.
// On denial it writes 429 Too Many Requests with standard headers.
// On Redis failure it fails open.
func RateLimit(l *limiter.Limiter, cfg config.LimiterConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			ip := extractIP(r, cfg.IPHeader)
			userID := r.Header.Get(cfg.UserIDHeader)
			apiKey := r.Header.Get(cfg.APIKeyHeader)

			instanceID := os.Getenv("INSTANCE_ID")
			if instanceID == "" {
				h, _ := os.Hostname()
				instanceID = h
			}

			decision, err := l.Allow(r.Context(), ip, userID, apiKey)
			if err != nil {
				slog.Error("rate limiter error", "err", err)
				metrics.LimiterErrors.Inc()

				// Fail-open
				next.ServeHTTP(w, r)
				return
			}

			// Record latency
			metrics.RedisLatency.Observe(float64(decision.LatencyUs))

			// Always attach headers
			writeHeaders(w, decision)

			// Denied case
			if !decision.Allowed {
				metrics.RequestsDenied.WithLabelValues(decision.DenyTier).Inc()

				retryMs := int64(0)
				if len(decision.Results) > 0 {
					retryMs = decision.Results[0].RetryAfterMs
				}

				slog.Info("rate limit exceeded",
					"ip", ip,
					"user_id", userID,
					"deny_tier", decision.DenyTier,
					"retry_after_ms", retryMs,
					"latency_us", decision.LatencyUs,
				)

				http.Error(w,
					`{"error":"rate_limit_exceeded","tier":"`+decision.DenyTier+`"}`,
					http.StatusTooManyRequests,
				)
				return
			}

			// Allowed case
			w.Header().Set("X-Instance-ID", instanceID)
			metrics.RequestsAllowed.WithLabelValues(identityTier(userID, apiKey)).Inc()
			next.ServeHTTP(w, r)
		})
	}
}

// writeHeaders sets rate limit headers based on aggregated result.
func writeHeaders(w http.ResponseWriter, d *limiter.Decision) {
	if len(d.Results) == 0 {
		return
	}

	r := d.Results[0] // single aggregated result

	// Remaining tokens
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(r.Remaining, 10))

	// Retry headers (only when denied)
	if !d.Allowed {
		retryAfterSec := (r.RetryAfterMs + 999) / 1000 // round up
		if retryAfterSec < 1 {
			retryAfterSec = 1
		}

		w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSec, 10))
		w.Header().Set("X-RateLimit-RetryAfter-Ms", strconv.FormatInt(r.RetryAfterMs, 10))
	}

	// Reset timestamp
	if r.ResetAtMs > 0 {
		resetTime := time.UnixMilli(r.ResetAtMs)
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
	}

	// Optional RFC-style headers (recommended)
	w.Header().Set("RateLimit-Remaining", strconv.FormatInt(r.Remaining, 10))
	w.Header().Set("RateLimit-Reset", strconv.FormatInt(r.ResetAtMs/1000, 10))

	w.Header().Set("Content-Type", "application/json")
}

// extractIP returns the client IP using headers or RemoteAddr.
func extractIP(r *http.Request, ipHeader string) string {
	if ipHeader != "" {
		if v := r.Header.Get(ipHeader); v != "" {
			// Handle X-Forwarded-For (comma-separated)
			for i, c := range v {
				if c == ',' {
					return v[:i]
				}
			}
			return v
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// identityTier determines label for metrics.
func identityTier(userID, apiKey string) string {
	switch {
	case userID != "":
		return "user"
	case apiKey != "":
		return "api_key"
	default:
		return "anonymous"
	}
}
