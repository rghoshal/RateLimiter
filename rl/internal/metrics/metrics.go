// Package metrics registers Prometheus metrics for the rate limiter.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsAllowed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ratelimiter_requests_allowed_total",
		Help: "Requests that passed all rate limit tiers.",
	}, []string{"identity_tier"})

	RequestsDenied = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ratelimiter_requests_denied_total",
		Help: "Requests denied by the rate limiter, labelled by the denying tier.",
	}, []string{"deny_tier"})

	LimiterErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ratelimiter_redis_errors_total",
		Help: "Redis errors during limit checks (fail-open path).",
	})

	RedisLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "ratelimiter_redis_latency_microseconds",
		Help:    "End-to-end Redis round-trip for a limit decision (µs).",
		Buckets: []float64{50, 100, 200, 400, 800, 1500, 3000, 6000, 12000},
	})

	TokensRemaining = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ratelimiter_tokens_remaining",
		Help: "Approximate tokens remaining per tier (sampled on allow path).",
	}, []string{"tier"})
)
