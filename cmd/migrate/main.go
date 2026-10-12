package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
	"github.com/nigelnunes/durable-flow/migrations"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := migrateWhenReady(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	for _, version := range result.Applied {
		fmt.Printf("applied %s\n", version)
	}
	for _, version := range result.Skipped {
		fmt.Printf("skip %s (already applied)\n", version)
	}
	fmt.Println("migrations up to date")
}

// migrateWhenReady retries only while Postgres is still starting.
// A SQL error fails immediately.
func migrateWhenReady(ctx context.Context, databaseURL string) (store.Result, error) {
	deadline := time.Now().Add(30 * time.Second)
	var (
		result store.Result
		err    error
	)
	for {
		result, err = store.Migrate(ctx, databaseURL, migrations.FS)
		if err == nil || !strings.Contains(err.Error(), "connect:") || time.Now().After(deadline) {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
