package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

// RunScheduler fires due timers until ctx is cancelled.
func RunScheduler(ctx context.Context, st *store.Store, interval time.Duration) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	fire := func() {
		if _, err := st.FireDueTimers(ctx, 100); err != nil && ctx.Err() == nil {
			slog.Error("fire due timers", "err", err)
		}
	}
	fire()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fire()
		}
	}
}
