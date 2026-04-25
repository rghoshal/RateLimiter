package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Initialize store
	store := NewStore()
	slog.Info("Ecommerce backend initialized")

	// ── Routes ─────────────────────────────────────────────────────────────────

	mux := http.NewServeMux()

	// Health check (exempt from rate limiting)
	mux.HandleFunc("GET /healthz", store.Health)

	// Products (read-heavy, high limit)
	mux.HandleFunc("GET /api/products", store.ListProducts)
	mux.HandleFunc("GET /api/products/{id}", store.GetProduct)

	// Cart operations (user-tier limited)
	mux.HandleFunc("GET /api/cart", store.GetCart)
	mux.HandleFunc("POST /api/cart", store.AddToCart)
	mux.HandleFunc("DELETE /api/cart", store.ClearCart)

	// Checkout (most restrictive - important operation)
	mux.HandleFunc("POST /api/checkout", store.Checkout)
	mux.HandleFunc("GET /api/orders", store.GetOrders)

	addr := os.Getenv("BACKEND_ADDR")
	if addr == "" {
		addr = ":9000"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background
	go func() {
		slog.Info("Ecommerce backend listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// ── Graceful shutdown ──────────────────────────────────────────────────────

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down ecommerce backend...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(1)
	}

	slog.Info("Ecommerce backend stopped")
}
