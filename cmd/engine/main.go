package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/nigelnunes/durable-flow/internal/api"
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

	ctx := context.Background()
	if _, err := store.Migrate(ctx, databaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	st, err := store.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.NewHandler(st, demoKey),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("engine listening", "addr", srv.Addr)
	return srv.ListenAndServe()
}
