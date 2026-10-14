package store

import (
	"context"
	"fmt"
	"time"
)

// DeleteOldCompleted removes completed workflows whose updated_at is older than age.
// Events, tasks, and timers cascade with the workflow row.
func (s *Store) DeleteOldCompleted(ctx context.Context, age time.Duration) (int64, error) {
	if age <= 0 {
		age = 24 * time.Hour
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM workflows
		WHERE status = 'completed'
		  AND updated_at < now() - ($1::bigint * interval '1 millisecond')
	`, age.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("delete old workflows: %w", err)
	}
	return tag.RowsAffected(), nil
}
