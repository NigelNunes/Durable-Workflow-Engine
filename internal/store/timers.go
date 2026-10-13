package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nigelnunes/durable-flow/internal/history"
)

// FireDueTimers fires up to limit timers whose fire_at has passed.
// Each timer is locked and applied in its own transaction.
func (s *Store) FireDueTimers(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	n := 0
	for n < limit {
		ok, err := s.fireOneTimer(ctx)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, nil
		}
		n++
	}
	return n, nil
}

func (s *Store) fireOneTimer(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin fire timer: %w", err)
	}
	defer tx.Rollback(ctx)

	var workflowID string
	var seq int
	err = tx.QueryRow(ctx, `
		SELECT workflow_id, command_seq
		FROM timers
		WHERE NOT fired AND fire_at <= now()
		ORDER BY fire_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&workflowID, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select due timer: %w", err)
	}

	state, err := lockWorkflow(ctx, tx, workflowID)
	if err != nil {
		return false, err
	}
	if state.Status == "running" {
		data, err := json.Marshal(struct {
			CommandSeq int `json:"command_seq"`
		}{CommandSeq: seq})
		if err != nil {
			return false, fmt.Errorf("encode timer fired: %w", err)
		}
		if err := appendEvents(ctx, tx, workflowID, NewEvent{Type: history.TimerFired, Data: data}); err != nil {
			return false, err
		}
		if err := enqueueWorkflowTask(ctx, tx, workflowID); err != nil {
			return false, err
		}
	}
	if err := markTimerFired(ctx, tx, workflowID, seq); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit fire timer: %w", err)
	}
	return true, nil
}

func markTimerFired(ctx context.Context, tx pgx.Tx, workflowID string, seq int) error {
	tag, err := tx.Exec(ctx, `
		UPDATE timers SET fired = true
		WHERE workflow_id = $1 AND command_seq = $2 AND NOT fired
	`, workflowID, seq)
	if err != nil {
		return fmt.Errorf("mark timer fired: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark timer fired: timer %s/%d was already fired", workflowID, seq)
	}
	return nil
}
