package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ArmChaos stores one chaos action on a worker. An empty workerID selects an
// alive worker. The action is delivered once, on that worker's next heartbeat.
func (s *Store) ArmChaos(ctx context.Context, workerID, value string, aliveFor time.Duration) (string, error) {
	if aliveFor <= 0 {
		aliveFor = 9 * time.Second
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin arm chaos: %w", err)
	}
	defer tx.Rollback(ctx)

	id := workerID
	if id == "" {
		err = tx.QueryRow(ctx, `
			SELECT id FROM workers
			WHERE last_heartbeat > now() - ($1::bigint * interval '1 millisecond')
			ORDER BY last_heartbeat DESC
			LIMIT 1
			FOR UPDATE
		`, aliveFor.Milliseconds()).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoAliveWorker
		}
		if err != nil {
			return "", fmt.Errorf("pick alive worker: %w", err)
		}
	} else {
		var alive bool
		err = tx.QueryRow(ctx, `
			SELECT last_heartbeat > now() - ($2::bigint * interval '1 millisecond')
			FROM workers WHERE id = $1 FOR UPDATE
		`, id, aliveFor.Milliseconds()).Scan(&alive)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrWorkerNotFound
		}
		if err != nil {
			return "", fmt.Errorf("lock chaos worker: %w", err)
		}
		if !alive {
			return "", ErrNoAliveWorker
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE workers SET pending_chaos = $2 WHERE id = $1`, id, value); err != nil {
		return "", fmt.Errorf("store chaos: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit arm chaos: %w", err)
	}
	return id, nil
}

// HasWorkflow reports whether a workflow id is already stored.
func (s *Store) HasWorkflow(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check workflow: %w", err)
	}
	return exists, nil
}

// CountRunning returns workflows whose status is running.
func (s *Store) CountRunning(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE status = 'running'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count running workflows: %w", err)
	}
	return n, nil
}
