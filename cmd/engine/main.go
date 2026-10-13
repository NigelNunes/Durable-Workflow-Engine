package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/nigelnunes/durable-flow/internal/api"
	"github.com/nigelnunes/durable-flow/internal/engine"
	"github.com/nigelnunes/durable-flow/internal/store"
	"github.com/nigelnunes/durable-flow/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("engine stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	demoKey := os.Getenv("DEMO_KEY")
	if demoKey == "" {
		return fmt.Errorf("DEMO_KEY is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	leaseTTL, err := envDuration("LEASE_TTL", 10*time.Second)
	if err != nil {
		return err
	}
	leaseTTL = store.NormalizeLeaseTTL(leaseTTL)
	heartbeatInterval, err := envDuration("HEARTBEAT_INTERVAL", 3*time.Second)
	if err != nil {
		return err
	}
	reaperInterval, err := envDuration("REAPER_INTERVAL", time.Second)
	if err != nil {
		return err
	}

	ctx := context.Background()
	if _, err := store.Migrate(ctx, databaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	st, err := store.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	reapCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go engine.RunReaper(reapCtx, st, reaperInterval)

	srv := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(st, api.Config{
			DemoKey:           demoKey,
			LeaseTTL:          leaseTTL,
			HeartbeatInterval: heartbeatInterval,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("engine listening", "addr", srv.Addr)
	return srv.ListenAndServe()
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}
