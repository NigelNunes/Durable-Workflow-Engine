// Package engine runs the background loops that sit beside the HTTP API.
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

// RunReaper returns expired task leases to the queue until ctx is cancelled.
func RunReaper(ctx context.Context, st *store.Store, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := st.ReapExpired(ctx, 100); err != nil && ctx.Err() == nil {
				slog.Error("reap expired tasks", "err", err)
			}
		}
	}
}
