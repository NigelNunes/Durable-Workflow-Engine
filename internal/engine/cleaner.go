package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

// RunCleaner deletes completed workflows older than 24 hours until ctx is cancelled.
func RunCleaner(ctx context.Context, st *store.Store, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	clean := func() {
		if _, err := st.DeleteOldCompleted(ctx, 24*time.Hour); err != nil && ctx.Err() == nil {
			slog.Error("delete old workflows", "err", err)
		}
	}
	clean()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}
