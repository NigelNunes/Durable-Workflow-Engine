package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nigelnunes/durable-flow/internal/history"
)

const pollWaitSlice = 20 * time.Millisecond

// PolledWorkflow is the workflow snapshot returned with a workflow task.
type PolledWorkflow struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Input json.RawMessage `json:"input"`
}

// PolledTask is one task a worker has just leased.
type PolledTask struct {
	TaskID     int64
	Kind       string
	LeaseToken int64

	Workflow   PolledWorkflow
	History    []Event
	HistoryLen int

	WorkflowID     string
	CommandSeq     int
	ActivityName   string
	Input          json.RawMessage
	Attempt        int
	IdempotencyKey string
}

// LeaseSeconds rounds d up to whole seconds, with a floor of 1.
// The lease SQL stores the TTL with make_interval(secs => ...).
func LeaseSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}

// NormalizeLeaseTTL returns d rounded up to the whole seconds the database stores.
func NormalizeLeaseTTL(d time.Duration) time.Duration {
	return time.Duration(LeaseSeconds(d)) * time.Second
}

// RegisterWorker inserts a worker and returns its id.
func (s *Store) RegisterWorker(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("worker name is required")
	}
	id, err := newWorkflowID()
	if err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO workers (id, name) VALUES ($1, $2)`, id, name); err != nil {
		return "", fmt.Errorf("register worker: %w", err)
	}
	return id, nil
}

// HeartbeatWorker records that the worker is alive and extends every lease it holds.
func (s *Store) HeartbeatWorker(ctx context.Context, id string, leaseTTL time.Duration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin worker heartbeat: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE workers SET last_heartbeat = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("update worker heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkerNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET lease_expires_at = now() + make_interval(secs => $2), updated_at = now()
		WHERE status = 'leased' AND lease_owner = $1
	`, id, LeaseSeconds(leaseTTL)); err != nil {
		return fmt.Errorf("extend worker leases: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit worker heartbeat: %w", err)
	}
	return nil
}

// PollTask leases one pending task whose kind is in kinds.
// It tries until wait elapses. The sleep between tries is outside any transaction.
// ok is false when the queue is empty. ErrWorkerNotFound means workerID is not registered.
func (s *Store) PollTask(ctx context.Context, workerID string, kinds []string, leaseTTL, wait time.Duration) (PolledTask, bool, error) {
	if err := s.ensureWorker(ctx, workerID); err != nil {
		return PolledTask{}, false, err
	}
	deadline := time.Now().Add(wait)
	for {
		task, ok, err := s.tryPoll(ctx, workerID, kinds, leaseTTL)
		if err != nil || ok {
			return task, ok, err
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return PolledTask{}, false, nil
		}
		if err := sleepUntil(ctx, deadline); err != nil {
			return PolledTask{}, false, err
		}
	}
}

func (s *Store) ensureWorker(ctx context.Context, id string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workers WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check worker: %w", err)
	}
	if !exists {
		return ErrWorkerNotFound
	}
	return nil
}

func (s *Store) tryPoll(ctx context.Context, workerID string, kinds []string, leaseTTL time.Duration) (PolledTask, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PolledTask{}, false, fmt.Errorf("begin poll: %w", err)
	}
	defer tx.Rollback(ctx)

	var row leasedRow
	err = tx.QueryRow(ctx, `
		UPDATE tasks SET
			status = 'leased',
			lease_owner = $1,
			lease_token = lease_token + 1,
			lease_expires_at = now() + make_interval(secs => $2),
			updated_at = now()
		WHERE id = (
			SELECT id FROM tasks
			WHERE status = 'pending' AND run_at <= now() AND kind = ANY($3)
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, workflow_id, kind, command_seq, activity_name, payload::text, attempt, lease_token
	`, workerID, LeaseSeconds(leaseTTL), kinds).Scan(
		&row.ID, &row.WorkflowID, &row.Kind, &row.CommandSeq, &row.ActivityName, &row.Payload, &row.Attempt, &row.LeaseToken,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PolledTask{}, false, nil
	}
	if err != nil {
		return PolledTask{}, false, fmt.Errorf("poll task: %w", err)
	}

	task := PolledTask{TaskID: row.ID, Kind: row.Kind, LeaseToken: row.LeaseToken}
	switch row.Kind {
	case "workflow":
		wf, events, historyLen, err := loadWorkflowPoll(ctx, tx, row.WorkflowID)
		if err != nil {
			return PolledTask{}, false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks SET history_len_at_dispatch = $2 WHERE id = $1
		`, row.ID, historyLen); err != nil {
			return PolledTask{}, false, fmt.Errorf("record history_len_at_dispatch: %w", err)
		}
		task.Workflow = wf
		task.History = events
		task.HistoryLen = historyLen
	case "activity":
		if row.CommandSeq == nil {
			return PolledTask{}, false, fmt.Errorf("activity task %d missing command_seq", row.ID)
		}
		name := ""
		if row.ActivityName != nil {
			name = *row.ActivityName
		}
		payload := json.RawMessage("null")
		if row.Payload != nil {
			payload = json.RawMessage(*row.Payload)
		}
		task.WorkflowID = row.WorkflowID
		task.CommandSeq = *row.CommandSeq
		task.ActivityName = name
		task.Input = payload
		task.Attempt = row.Attempt
		task.IdempotencyKey = fmt.Sprintf("%s:%d", row.WorkflowID, *row.CommandSeq)
	default:
		return PolledTask{}, false, fmt.Errorf("unknown task kind %q", row.Kind)
	}

	if err := tx.Commit(ctx); err != nil {
		return PolledTask{}, false, fmt.Errorf("commit poll: %w", err)
	}
	return task, true, nil
}

type leasedRow struct {
	ID           int64
	WorkflowID   string
	Kind         string
	CommandSeq   *int
	ActivityName *string
	Payload      *string
	Attempt      int
	LeaseToken   int64
}

func loadWorkflowPoll(ctx context.Context, tx pgx.Tx, workflowID string) (PolledWorkflow, []Event, int, error) {
	var (
		wf         PolledWorkflow
		input      string
		historyLen int
	)
	err := tx.QueryRow(ctx, `
		SELECT id, type, input::text, history_len FROM workflows WHERE id = $1 FOR UPDATE
	`, workflowID).Scan(&wf.ID, &wf.Type, &input, &historyLen)
	if err != nil {
		return PolledWorkflow{}, nil, 0, fmt.Errorf("load polled workflow: %w", err)
	}
	wf.Input = json.RawMessage(input)

	rows, err := tx.Query(ctx, `
		SELECT seq, type, data::text, created_at
		FROM workflow_events
		WHERE workflow_id = $1
		ORDER BY seq
	`, workflowID)
	if err != nil {
		return PolledWorkflow{}, nil, 0, fmt.Errorf("load polled history: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var (
			event Event
			data  string
		)
		if err := rows.Scan(&event.Seq, &event.Type, &data, &event.CreatedAt); err != nil {
			return PolledWorkflow{}, nil, 0, fmt.Errorf("load polled history: %w", err)
		}
		event.Data = json.RawMessage(data)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return PolledWorkflow{}, nil, 0, fmt.Errorf("load polled history: %w", err)
	}
	return wf, events, historyLen, nil
}

// HeartbeatTask extends one lease when the token matches the leased row.
// Any other state returns ErrLeaseMismatch and writes nothing.
func (s *Store) HeartbeatTask(ctx context.Context, taskID, leaseToken int64, leaseTTL time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET lease_expires_at = now() + make_interval(secs => $3), updated_at = now()
		WHERE id = $1 AND status = 'leased' AND lease_token = $2
	`, taskID, leaseToken, LeaseSeconds(leaseTTL))
	if err != nil {
		return fmt.Errorf("heartbeat task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseMismatch
	}
	return nil
}

// ReapExpired returns up to limit expired leases to the queue.
// Each task is locked and reset in its own transaction, after the workflow row is locked.
// The lease token is left unchanged so the next poll increases it.
// An activity whose next redelivery would pass maxRedeliveries fails instead.
func (s *Store) ReapExpired(ctx context.Context, limit, maxRedeliveries int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if maxRedeliveries <= 0 {
		maxRedeliveries = DefaultMaxRedeliveries
	}
	n := 0
	for n < limit {
		ok, err := s.reapOne(ctx, maxRedeliveries)
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

func (s *Store) reapOne(ctx context.Context, maxRedeliveries int) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin reap: %w", err)
	}
	defer tx.Rollback(ctx)

	var row expiredTask
	err = tx.QueryRow(ctx, `
		SELECT id, workflow_id, kind, command_seq, lease_owner, redeliveries
		FROM tasks
		WHERE status = 'leased' AND lease_expires_at < now()
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&row.ID, &row.WorkflowID, &row.Kind, &row.CommandSeq, &row.Owner, &row.Redeliveries)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select expired task: %w", err)
	}

	if _, err := lockWorkflow(ctx, tx, row.WorkflowID); err != nil {
		return false, err
	}
	next := row.Redeliveries + 1
	owner := ""
	if row.Owner != nil {
		owner = *row.Owner
	}
	if err := appendLeaseExpired(ctx, tx, row, owner, next); err != nil {
		return false, err
	}
	poison := row.Kind == "activity" && next > maxRedeliveries
	if poison {
		if err := failRedeliveries(ctx, tx, row, next); err != nil {
			return false, err
		}
	} else if err := requeueExpired(ctx, tx, row.ID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit reap: %w", err)
	}
	return true, nil
}

type expiredTask struct {
	ID           int64
	WorkflowID   string
	Kind         string
	CommandSeq   *int
	Owner        *string
	Redeliveries int
}

func appendLeaseExpired(ctx context.Context, tx pgx.Tx, row expiredTask, owner string, redeliveries int) error {
	if row.Kind == "workflow" {
		data, err := json.Marshal(struct {
			WorkerID string `json:"worker_id"`
		}{WorkerID: owner})
		if err != nil {
			return fmt.Errorf("encode workflow lease expired: %w", err)
		}
		return appendEvents(ctx, tx, row.WorkflowID, NewEvent{Type: history.WorkflowTaskLeaseExpired, Data: data})
	}
	if row.CommandSeq == nil {
		return fmt.Errorf("activity task %d missing command_seq", row.ID)
	}
	data, err := json.Marshal(struct {
		CommandSeq   int    `json:"command_seq"`
		WorkerID     string `json:"worker_id"`
		Redeliveries int    `json:"redeliveries"`
	}{CommandSeq: *row.CommandSeq, WorkerID: owner, Redeliveries: redeliveries})
	if err != nil {
		return fmt.Errorf("encode activity lease expired: %w", err)
	}
	return appendEvents(ctx, tx, row.WorkflowID, NewEvent{Type: history.ActivityLeaseExpired, Data: data})
}

func requeueExpired(ctx context.Context, tx pgx.Tx, taskID int64) error {
	tag, err := tx.Exec(ctx, `
		UPDATE tasks SET
			status = 'pending',
			run_at = now(),
			redeliveries = redeliveries + 1,
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE id = $1 AND status = 'leased'
	`, taskID)
	if err != nil {
		return fmt.Errorf("reset expired task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reset expired task: task %d was not leased", taskID)
	}
	return nil
}

func failRedeliveries(ctx context.Context, tx pgx.Tx, row expiredTask, redeliveries int) error {
	if row.CommandSeq == nil {
		return fmt.Errorf("activity task %d missing command_seq", row.ID)
	}
	event, err := activityResultEvent(*row.CommandSeq, true, nil, history.TooManyRedeliveries, true)
	if err != nil {
		return err
	}
	if err := appendEvents(ctx, tx, row.WorkflowID, event); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE tasks SET
			status = 'done',
			redeliveries = $2,
			lease_owner = NULL,
			lease_expires_at = NULL,
			last_error = $3,
			updated_at = now()
		WHERE id = $1 AND status = 'leased'
	`, row.ID, redeliveries, history.TooManyRedeliveries)
	if err != nil {
		return fmt.Errorf("fail redelivered activity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("fail redelivered activity: task %d was not leased", row.ID)
	}
	state, err := lockWorkflow(ctx, tx, row.WorkflowID)
	if err != nil {
		return err
	}
	if state.Status != "running" {
		return nil
	}
	return enqueueWorkflowTask(ctx, tx, row.WorkflowID)
}

func sleepUntil(ctx context.Context, deadline time.Time) error {
	d := pollWaitSlice
	if remain := time.Until(deadline); remain < d {
		d = remain
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
