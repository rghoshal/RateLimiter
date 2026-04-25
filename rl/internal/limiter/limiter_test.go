package limiter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/example/ratelimiter/internal/config"
	"github.com/example/ratelimiter/internal/limiter"
)

// newTestLimiter spins up an in-process Redis (miniredis) and returns a Limiter.
func newTestLimiter(t *testing.T, tiers []config.TierConfig) *limiter.Limiter {
	t.Helper()
	mr := miniredis.RunT(t)

	cfg := config.LimiterConfig{
		RedisAddr:    mr.Addr(),
		IPHeader:     "X-Real-IP",
		UserIDHeader: "X-User-ID",
		APIKeyHeader: "X-Api-Key",
		Tiers:        tiers,
	}

	l, err := limiter.New(cfg)
	if err != nil {
		t.Fatalf("limiter.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// ─── Basic allow/deny ─────────────────────────────────────────────────────────

func TestAllowUnderCapacity(t *testing.T) {
	l := newTestLimiter(t, []config.TierConfig{{
		Label:      "ip",
		Capacity:   10,
		RefillRate: 1,
		Requested:  1,
		WindowSize: 60 * time.Second,
		BurstLimit: 10,
		TTL:        5 * time.Minute,
	}})

	for i := 0; i < 10; i++ {
		d, err := l.Allow(context.Background(), "1.2.3.4", "", "")
		if err != nil {
			t.Fatalf("Allow #%d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("request #%d should be allowed but was denied (tier=%s)", i, d.DenyTier)
		}
	}
}

func TestDenyAfterCapacityExhausted(t *testing.T) {
	const cap = 5
	l := newTestLimiter(t, []config.TierConfig{{
		Label:      "ip",
		Capacity:   cap,
		RefillRate: 0.01, // extremely slow refill — won't matter in test
		Requested:  1,
		WindowSize: 60 * time.Second,
		BurstLimit: 100, // disable burst guard for this test
		TTL:        5 * time.Minute,
	}})

	ctx := context.Background()
	for i := 0; i < cap; i++ {
		d, _ := l.Allow(ctx, "10.0.0.1", "", "")
		if !d.Allowed {
			t.Fatalf("request %d/%d should be allowed", i+1, cap)
		}
	}
	// (cap+1)th request must be denied
	d, err := l.Allow(ctx, "10.0.0.1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("expected deny after capacity exhausted, got allow")
	}
	if d.DenyTier != "ip" {
		t.Fatalf("expected deny_tier=ip, got %q", d.DenyTier)
	}
}

// ─── Burst guard ──────────────────────────────────────────────────────────────

func TestBurstGuardDeniesWhenWindowFull(t *testing.T) {
	const burstLimit = 3
	l := newTestLimiter(t, []config.TierConfig{{
		Label:      "ip",
		Capacity:   1000,     // plenty of tokens
		RefillRate: 500,
		Requested:  1,
		WindowSize: 60 * time.Second,
		BurstLimit: burstLimit,
		TTL:        5 * time.Minute,
	}})

	ctx := context.Background()
	for i := 0; i < burstLimit; i++ {
		d, _ := l.Allow(ctx, "5.6.7.8", "", "")
		if !d.Allowed {
			t.Fatalf("request %d should be allowed (burst not full)", i+1)
		}
	}
	d, _ := l.Allow(ctx, "5.6.7.8", "", "")
	if d.Allowed {
		t.Fatal("burst guard should have denied this request")
	}
}

// ─── Tier isolation ───────────────────────────────────────────────────────────

func TestIPAndUserTiersAreIndependent(t *testing.T) {
	l := newTestLimiter(t, []config.TierConfig{
		{
			Label: "ip", Capacity: 2, RefillRate: 0.01,
			Requested: 1, WindowSize: time.Minute, BurstLimit: 100, TTL: time.Minute,
		},
		{
			Label: "user", Capacity: 100, RefillRate: 50,
			Requested: 1, WindowSize: time.Minute, BurstLimit: 100, TTL: time.Minute,
		},
	})

	ctx := context.Background()
	// Exhaust the IP tier for this IP
	l.Allow(ctx, "9.9.9.9", "user-abc", "")
	l.Allow(ctx, "9.9.9.9", "user-abc", "")

	// Now denied at IP level
	d, _ := l.Allow(ctx, "9.9.9.9", "user-abc", "")
	if d.Allowed {
		t.Fatal("expected deny at ip tier")
	}
	if d.DenyTier != "ip" {
		t.Fatalf("expected ip deny, got %q", d.DenyTier)
	}

	// Same user from a different IP should still be allowed (user bucket not exhausted)
	d2, _ := l.Allow(ctx, "1.1.1.1", "user-abc", "")
	if !d2.Allowed {
		t.Fatalf("user from different IP should be allowed, denied by %q", d2.DenyTier)
	}
}

// ─── RetryAfter ───────────────────────────────────────────────────────────────

func TestRetryAfterPositiveOnDeny(t *testing.T) {
	l := newTestLimiter(t, []config.TierConfig{{
		Label:      "ip",
		Capacity:   1,
		RefillRate: 1, // 1 token/sec → retry after ~1s
		Requested:  1,
		WindowSize: 60 * time.Second,
		BurstLimit: 100,
		TTL:        5 * time.Minute,
	}})

	ctx := context.Background()
	l.Allow(ctx, "2.2.2.2", "", "") // consume the only token
	d, _ := l.Allow(ctx, "2.2.2.2", "", "")

	if d.Allowed {
		t.Fatal("should be denied")
	}
	if d.MaxRetryAfterMs() <= 0 {
		t.Fatalf("retry_after_ms should be positive, got %d", d.MaxRetryAfterMs())
	}
}

// ─── Concurrent safety ────────────────────────────────────────────────────────

func TestConcurrentRequestsDoNotExceedCapacity(t *testing.T) {
	const (
		capacity   = 50
		goroutines = 20
		reqsEach   = 10
	)

	l := newTestLimiter(t, []config.TierConfig{{
		Label:      "ip",
		Capacity:   capacity,
		RefillRate: 0.001, // refill disabled for test
		Requested:  1,
		WindowSize: 60 * time.Second,
		BurstLimit: capacity * 10,
		TTL:        5 * time.Minute,
	}})

	ctx := context.Background()
	allowed := make(chan bool, goroutines*reqsEach)

	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < reqsEach; i++ {
				d, err := l.Allow(ctx, "concurrent-test", "", "")
				if err != nil {
					allowed <- false
					continue
				}
				allowed <- d.Allowed
			}
		}()
	}

	total := goroutines * reqsEach
	allowedCount := 0
	for i := 0; i < total; i++ {
		if <-allowed {
			allowedCount++
		}
	}

	if allowedCount > capacity {
		t.Fatalf("allowed %d requests but capacity is %d — atomicity violated", allowedCount, capacity)
	}
	t.Logf("allowed %d/%d (capacity=%d) ✓", allowedCount, total, capacity)
}

// ─── Multi-tier deny propagation ─────────────────────────────────────────────

func TestFirstTierDenyWins(t *testing.T) {
	l := newTestLimiter(t, []config.TierConfig{
		// global tier will deny immediately (cap=0 would panic; use 1 and exhaust)
		{
			Label: "global", Capacity: 1, RefillRate: 0.001,
			Requested: 1, WindowSize: time.Minute, BurstLimit: 100, TTL: time.Minute,
		},
		{
			Label: "ip", Capacity: 100, RefillRate: 50,
			Requested: 1, WindowSize: time.Minute, BurstLimit: 100, TTL: time.Minute,
		},
	})

	ctx := context.Background()
	l.Allow(ctx, "3.3.3.3", "", "") // consume global token

	d, _ := l.Allow(ctx, "3.3.3.3", "", "")
	if d.Allowed {
		t.Fatal("expected denial")
	}
	if d.DenyTier != "global" {
		t.Fatalf("expected global deny, got %q", d.DenyTier)
	}
}

// ─── Benchmark ───────────────────────────────────────────────────────────────

func BenchmarkAllow(b *testing.B) {
	mr := miniredis.RunT(b)
	cfg := config.LimiterConfig{
		RedisAddr:    mr.Addr(),
		IPHeader:     "X-Real-IP",
		UserIDHeader: "X-User-ID",
		APIKeyHeader: "X-Api-Key",
		Tiers: []config.TierConfig{{
			Label:      "ip",
			Capacity:   1e9,
			RefillRate: 1e9,
			Requested:  1,
			WindowSize: time.Minute,
			BurstLimit: 1e9,
			TTL:        5 * time.Minute,
		}},
	}
	l, err := limiter.New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()

	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("10.0.%d.%d", (i/256)%256, i%256)
			_, _ = l.Allow(ctx, ip, "", "")
			i++
		}
	})
}

// helper to ping miniredis via go-redis
func ping(addr string) error {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	return rdb.Ping(context.Background()).Err()
}
