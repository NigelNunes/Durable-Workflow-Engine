package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nigelnunes/durable-flow/internal/history"
)

// WorkflowCommand is one command from a workflow task completion.
type WorkflowCommand struct {
	Type        string
	Seq         int
	Name        string
	Input       json.RawMessage
	RetryPolicy json.RawMessage
	MaxAttempts int
	Result      json.RawMessage
	Error       string
	FireAt      time.Time
}

// CompleteWorkflowTask applies commands when the workflow history still matches
// the length recorded at poll. A mismatch throws the commands away and queues
// a fresh workflow task. The task row is locked before the workflow row.
func (s *Store) CompleteWorkflowTask(ctx context.Context, taskID, leaseToken int64, workerID string, commands []WorkflowCommand) error {
	if err := checkCommands(commands); err != nil {
		return err
	}
	return s.withLease(ctx, taskID, leaseToken, workerID, func(tx pgx.Tx, task taskRow) error {
		if task.Kind != "workflow" {
			return fmt.Errorf("%w: task %d is a %s task", ErrBadCommand, taskID, task.Kind)
		}
		state, err := lockWorkflow(ctx, tx, task.WorkflowID)
		if err != nil {
			return err
		}
		if task.Dispatched == nil || *task.Dispatched != state.HistoryLen {
			if err := markTaskDone(ctx, tx, taskID); err != nil {
				return err
			}
			return enqueueWorkflowTask(ctx, tx, task.WorkflowID)
		}
		for _, cmd := range commands {
			if err := applyWorkflowCommand(ctx, tx, task.WorkflowID, cmd); err != nil {
				return err
			}
		}
		return markTaskDone(ctx, tx, taskID)
	})
}

// CompleteActivityTask records ActivityCompleted once, then queues a workflow
// task when one is not already open. A second result for the same command is
// ignored and the task is marked done.
func (s *Store) CompleteActivityTask(ctx context.Context, taskID, leaseToken int64, workerID string, result json.RawMessage) error {
	return s.withLease(ctx, taskID, leaseToken, workerID, func(tx pgx.Tx, task taskRow) error {
		return finishActivity(ctx, tx, task, false, result, "", false)
	})
}

// FailActivityTask retries a retryable error while attempts remain.
// Otherwise it records a terminal ActivityFailed.
func (s *Store) FailActivityTask(ctx context.Context, taskID, leaseToken int64, workerID, message string, nonRetryable bool) error {
	return s.withLease(ctx, taskID, leaseToken, workerID, func(tx pgx.Tx, task taskRow) error {
		return finishActivity(ctx, tx, task, true, nil, message, nonRetryable)
	})
}

// FailWorkflowTask closes the workflow. nonDeterminism is stored on the event
// so a code mismatch can be told apart from a panic in the workflow function.
func (s *Store) FailWorkflowTask(ctx context.Context, taskID, leaseToken int64, workerID, message string, nonDeterminism bool) error {
	return s.withLease(ctx, taskID, leaseToken, workerID, func(tx pgx.Tx, task taskRow) error {
		if task.Kind != "workflow" {
			return fmt.Errorf("%w: task %d is a %s task", ErrBadCommand, taskID, task.Kind)
		}
		state, err := lockWorkflow(ctx, tx, task.WorkflowID)
		if err != nil {
			return err
		}
		if state.Status == "running" {
			data, err := json.Marshal(struct {
				Error          string `json:"error"`
				NonDeterminism bool   `json:"non_determinism"`
			}{Error: message, NonDeterminism: nonDeterminism})
			if err != nil {
				return fmt.Errorf("encode workflow failed: %w", err)
			}
			if err := appendEvents(ctx, tx, task.WorkflowID, NewEvent{Type: history.WorkflowFailed, Data: data}); err != nil {
				return err
			}
			if err := failWorkflow(ctx, tx, task.WorkflowID, message); err != nil {
				return err
			}
		}
		return markTaskDone(ctx, tx, task.ID)
	})
}

func checkCommands(commands []WorkflowCommand) error {
	for _, cmd := range commands {
		switch cmd.Type {
		case history.CmdScheduleActivity:
			if cmd.Seq < 1 || cmd.Name == "" {
				return fmt.Errorf("%w: schedule activity needs a seq and a name", ErrBadCommand)
			}
		case history.CmdCompleteWorkflow, history.CmdFailWorkflow:
		case history.CmdStartTimer:
			if cmd.Seq < 1 || cmd.FireAt.IsZero() {
				return fmt.Errorf("%w: start timer needs a seq and a fire_at", ErrBadCommand)
			}
		default:
			return fmt.Errorf("%w: %s", ErrUnsupportedCommand, cmd.Type)
		}
	}
	return nil
}

func finishActivity(ctx context.Context, tx pgx.Tx, task taskRow, failed bool, result json.RawMessage, message string, nonRetryable bool) error {
	if task.Kind != "activity" || task.CommandSeq == nil {
		return fmt.Errorf("%w: task %d is not an activity task", ErrBadCommand, task.ID)
	}
	state, err := lockWorkflow(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}
	done, err := activityTerminal(ctx, tx, task.WorkflowID, *task.CommandSeq)
	if err != nil {
		return err
	}
	if done {
		return markTaskDone(ctx, tx, task.ID)
	}
	if failed && !nonRetryable && task.Attempt < task.MaxAttempts {
		return scheduleActivityRetry(ctx, tx, task, message)
	}
	event, err := activityResultEvent(*task.CommandSeq, failed, result, message, nonRetryable)
	if err != nil {
		return err
	}
	if err := appendEvents(ctx, tx, task.WorkflowID, event); err != nil {
		return err
	}
	if failed {
		if err := setLastError(ctx, tx, task.ID, message); err != nil {
			return err
		}
	}
	if state.Status == "running" {
		if err := enqueueWorkflowTask(ctx, tx, task.WorkflowID); err != nil {
			return err
		}
	}
	return markTaskDone(ctx, tx, task.ID)
}

func scheduleActivityRetry(ctx context.Context, tx pgx.Tx, task taskRow, message string) error {
	policy, err := loadRetryPolicy(ctx, tx, task.WorkflowID, *task.CommandSeq)
	if err != nil {
		return err
	}
	delay := backoffDelay(task.Attempt, policy)
	var runAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE tasks SET
			status = 'pending',
			attempt = attempt + 1,
			run_at = now() + ($2::bigint * interval '1 millisecond'),
			last_error = $3,
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE id = $1
		RETURNING run_at
	`, task.ID, delay.Milliseconds(), message).Scan(&runAt); err != nil {
		return fmt.Errorf("reschedule activity: %w", err)
	}
	data, err := json.Marshal(struct {
		CommandSeq int       `json:"command_seq"`
		Attempt    int       `json:"attempt"`
		Error      string    `json:"error"`
		NextRunAt  time.Time `json:"next_run_at"`
	}{
		CommandSeq: *task.CommandSeq,
		Attempt:    task.Attempt + 1,
		Error:      message,
		NextRunAt:  runAt,
	})
	if err != nil {
		return fmt.Errorf("encode activity retry: %w", err)
	}
	return appendEvents(ctx, tx, task.WorkflowID, NewEvent{Type: history.ActivityRetryScheduled, Data: data})
}

func loadRetryPolicy(ctx context.Context, tx pgx.Tx, workflowID string, seq int) (retryPolicy, error) {
	var raw string
	err := tx.QueryRow(ctx, `
		SELECT data::text FROM workflow_events
		WHERE workflow_id = $1 AND type = $2 AND data->>'command_seq' = $3
		ORDER BY seq DESC
		LIMIT 1
	`, workflowID, history.ActivityScheduled, strconv.Itoa(seq)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return retryPolicy{}, nil
	}
	if err != nil {
		return retryPolicy{}, fmt.Errorf("load retry policy: %w", err)
	}
	var body struct {
		RetryPolicy json.RawMessage `json:"retry_policy"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return retryPolicy{}, fmt.Errorf("decode activity scheduled: %w", err)
	}
	if len(body.RetryPolicy) == 0 || string(body.RetryPolicy) == "null" {
		return retryPolicy{}, nil
	}
	var policy retryPolicy
	if err := json.Unmarshal(body.RetryPolicy, &policy); err != nil {
		return retryPolicy{}, fmt.Errorf("decode retry policy: %w", err)
	}
	return policy, nil
}

func activityResultEvent(seq int, failed bool, result json.RawMessage, message string, nonRetryable bool) (NewEvent, error) {
	if failed {
		data, err := json.Marshal(struct {
			CommandSeq   int    `json:"command_seq"`
			Error        string `json:"error"`
			NonRetryable bool   `json:"non_retryable"`
		}{CommandSeq: seq, Error: message, NonRetryable: nonRetryable})
		if err != nil {
			return NewEvent{}, fmt.Errorf("encode activity failed: %w", err)
		}
		return NewEvent{Type: history.ActivityFailed, Data: data}, nil
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	data, err := json.Marshal(struct {
		CommandSeq int             `json:"command_seq"`
		Result     json.RawMessage `json:"result"`
	}{CommandSeq: seq, Result: result})
	if err != nil {
		return NewEvent{}, fmt.Errorf("encode activity completed: %w", err)
	}
	return NewEvent{Type: history.ActivityCompleted, Data: data}, nil
}

func applyWorkflowCommand(ctx context.Context, tx pgx.Tx, workflowID string, cmd WorkflowCommand) error {
	switch cmd.Type {
	case history.CmdScheduleActivity:
		return scheduleActivity(ctx, tx, workflowID, cmd)
	case history.CmdCompleteWorkflow:
		return completeWorkflow(ctx, tx, workflowID, cmd.Result)
	case history.CmdFailWorkflow:
		return failWorkflowCommand(ctx, tx, workflowID, cmd.Error)
	case history.CmdStartTimer:
		return startTimer(ctx, tx, workflowID, cmd)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedCommand, cmd.Type)
	}
}

func startTimer(ctx context.Context, tx pgx.Tx, workflowID string, cmd WorkflowCommand) error {
	fireAt := cmd.FireAt.UTC()
	data, err := json.Marshal(struct {
		CommandSeq int       `json:"command_seq"`
		FireAt     time.Time `json:"fire_at"`
	}{CommandSeq: cmd.Seq, FireAt: fireAt})
	if err != nil {
		return fmt.Errorf("encode timer started: %w", err)
	}
	if err := appendEvents(ctx, tx, workflowID, NewEvent{Type: history.TimerStarted, Data: data}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO timers (workflow_id, command_seq, fire_at)
		VALUES ($1, $2, $3)
	`, workflowID, cmd.Seq, fireAt); err != nil {
		return fmt.Errorf("insert timer: %w", err)
	}
	return nil
}

func scheduleActivity(ctx context.Context, tx pgx.Tx, workflowID string, cmd WorkflowCommand) error {
	input := cmd.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	retry := cmd.RetryPolicy
	if len(retry) == 0 {
		retry = json.RawMessage("null")
	}
	data, err := json.Marshal(struct {
		CommandSeq  int             `json:"command_seq"`
		Name        string          `json:"name"`
		Input       json.RawMessage `json:"input"`
		RetryPolicy json.RawMessage `json:"retry_policy"`
	}{CommandSeq: cmd.Seq, Name: cmd.Name, Input: input, RetryPolicy: retry})
	if err != nil {
		return fmt.Errorf("encode activity scheduled: %w", err)
	}
	if err := appendEvents(ctx, tx, workflowID, NewEvent{Type: history.ActivityScheduled, Data: data}); err != nil {
		return err
	}
	attempts := cmd.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (workflow_id, kind, command_seq, activity_name, payload, status, max_attempts)
		VALUES ($1, 'activity', $2, $3, $4::jsonb, 'pending', $5)
	`, workflowID, cmd.Seq, cmd.Name, string(input), attempts); err != nil {
		return fmt.Errorf("insert activity task: %w", err)
	}
	return nil
}

func completeWorkflow(ctx context.Context, tx pgx.Tx, workflowID string, result json.RawMessage) error {
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	data, err := json.Marshal(struct {
		Result json.RawMessage `json:"result"`
	}{Result: result})
	if err != nil {
		return fmt.Errorf("encode workflow completed: %w", err)
	}
	if err := appendEvents(ctx, tx, workflowID, NewEvent{Type: history.WorkflowCompleted, Data: data}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workflows
		SET status = 'completed', result = $2::jsonb, closed_at = now(), updated_at = now()
		WHERE id = $1
	`, workflowID, string(result)); err != nil {
		return fmt.Errorf("complete workflow: %w", err)
	}
	return nil
}

func failWorkflowCommand(ctx context.Context, tx pgx.Tx, workflowID, message string) error {
	data, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: message})
	if err != nil {
		return fmt.Errorf("encode workflow failed: %w", err)
	}
	if err := appendEvents(ctx, tx, workflowID, NewEvent{Type: history.WorkflowFailed, Data: data}); err != nil {
		return err
	}
	return failWorkflow(ctx, tx, workflowID, message)
}

func failWorkflow(ctx context.Context, tx pgx.Tx, workflowID, message string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE workflows
		SET status = 'failed', error = $2, closed_at = now(), updated_at = now()
		WHERE id = $1
	`, workflowID, message); err != nil {
		return fmt.Errorf("fail workflow: %w", err)
	}
	return nil
}

type taskRow struct {
	ID          int64
	WorkflowID  string
	Kind        string
	CommandSeq  *int
	Dispatched  *int
	Status      string
	LeaseToken  int64
	Attempt     int
	MaxAttempts int
}

func (s *Store) withLease(ctx context.Context, taskID, leaseToken int64, workerID string, fn func(pgx.Tx, taskRow) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin task completion: %w", err)
	}
	defer tx.Rollback(ctx)

	task, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if task.Status != "leased" || task.LeaseToken != leaseToken {
		if err := appendStaleRejection(ctx, tx, task, workerID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit stale completion: %w", err)
		}
		return ErrLeaseMismatch
	}
	if err := fn(tx, task); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task completion: %w", err)
	}
	return nil
}

func lockTask(ctx context.Context, tx pgx.Tx, taskID int64) (taskRow, error) {
	var task taskRow
	err := tx.QueryRow(ctx, `
		SELECT id, workflow_id, kind, command_seq, history_len_at_dispatch,
		       status, lease_token, attempt, max_attempts
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, taskID).Scan(
		&task.ID, &task.WorkflowID, &task.Kind, &task.CommandSeq, &task.Dispatched,
		&task.Status, &task.LeaseToken, &task.Attempt, &task.MaxAttempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskRow{}, ErrLeaseMismatch
	}
	if err != nil {
		return taskRow{}, fmt.Errorf("lock task: %w", err)
	}
	return task, nil
}

func appendStaleRejection(ctx context.Context, tx pgx.Tx, task taskRow, workerID string) error {
	seq := 0
	if task.CommandSeq != nil {
		seq = *task.CommandSeq
	}
	data, err := json.Marshal(struct {
		CommandSeq int    `json:"command_seq"`
		WorkerID   string `json:"worker_id"`
	}{CommandSeq: seq, WorkerID: workerID})
	if err != nil {
		return fmt.Errorf("encode stale completion: %w", err)
	}
	return appendEvents(ctx, tx, task.WorkflowID, NewEvent{Type: history.StaleCompletionRejected, Data: data})
}

func setLastError(ctx context.Context, tx pgx.Tx, taskID int64, message string) error {
	if _, err := tx.Exec(ctx, `UPDATE tasks SET last_error = $2, updated_at = now() WHERE id = $1`, taskID, message); err != nil {
		return fmt.Errorf("record last error: %w", err)
	}
	return nil
}

type workflowRow struct {
	HistoryLen int
	Status     string
}

func lockWorkflow(ctx context.Context, tx pgx.Tx, workflowID string) (workflowRow, error) {
	var row workflowRow
	err := tx.QueryRow(ctx, `
		SELECT history_len, status FROM workflows WHERE id = $1 FOR UPDATE
	`, workflowID).Scan(&row.HistoryLen, &row.Status)
	if err != nil {
		return workflowRow{}, fmt.Errorf("lock workflow: %w", err)
	}
	return row, nil
}

func markTaskDone(ctx context.Context, tx pgx.Tx, taskID int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = 'done', updated_at = now() WHERE id = $1
	`, taskID); err != nil {
		return fmt.Errorf("mark task done: %w", err)
	}
	return nil
}

func enqueueWorkflowTask(ctx context.Context, tx pgx.Tx, workflowID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO tasks (workflow_id, kind, status)
		VALUES ($1, 'workflow', 'pending')
	`, workflowID)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil
	}
	return fmt.Errorf("enqueue workflow task: %w", err)
}

func activityTerminal(ctx context.Context, tx pgx.Tx, workflowID string, seq int) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM workflow_events
			WHERE workflow_id = $1
			  AND type IN ($2, $3)
			  AND data->>'command_seq' = $4
		)
	`, workflowID, history.ActivityCompleted, history.ActivityFailed, strconv.Itoa(seq)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check activity result: %w", err)
	}
	return exists, nil
}
