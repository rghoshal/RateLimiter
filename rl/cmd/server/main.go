package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/ratelimiter/internal/config"
	"github.com/example/ratelimiter/internal/limiter"
	"github.com/example/ratelimiter/internal/middleware"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Handle healthcheck command
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		cfg := config.Default()
		if err := cfg.Validate(); err != nil {
			slog.Error("health check config validation failed", "err", err)
			os.Exit(1)
		}
		rl, err := limiter.New(cfg)
		if err != nil {
			slog.Error("health check failed: cannot connect to Redis", "err", err)
			os.Exit(1)
		}
		rl.Close()
		slog.Info("health check passed")
		os.Exit(0)
	}

	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}

	rl, err := limiter.New(cfg)
	if err != nil {
		slog.Error("limiter init failed", "err", err)
		os.Exit(1)
	}
	defer rl.Close()

	slog.Info("rate limiter connected to Redis", "addr", cfg.RedisAddr)

	// ── Backend Proxy Setup ────────────────────────────────────────────────────
	backendURL := os.Getenv("BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://backend:9000"
	}

	parsedBackendURL, err := url.Parse(backendURL)
	if err != nil {
		slog.Error("invalid backend URL", "err", err, "url", backendURL)
		os.Exit(1)
	}

	backendProxy := httputil.NewSingleHostReverseProxy(parsedBackendURL)
	slog.Info("rate limiter configured to proxy to backend", "backend", backendURL)

	// ── Routes ───────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// Health check (exempt from rate limiting)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Prometheus metrics endpoint
	mux.Handle("GET /metrics", promhttp.Handler())

	// API proxy: All /api/* requests are proxied to backend (rate limited)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		// ✅ CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-ID")

		// ✅ Handle preflight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		backendProxy.ServeHTTP(w, r)
	})

	// Admin endpoint: inspect current limits for a given IP
	mux.HandleFunc("GET /admin/limits", func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		if ip == "" {
			http.Error(w, `{"error":"ip required"}`, http.StatusBadRequest)
			return
		}
		d, err := rl.Allow(r.Context(), ip, "", "")
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	})

	// Apply rate limiting middleware to all API routes
	rateLimited := middleware.RateLimit(rl, cfg)(mux)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      rateLimited,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	go func() {
		slog.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}
