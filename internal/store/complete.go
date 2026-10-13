package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

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
}

// CompleteWorkflowTask applies commands when the workflow history still matches
// the length recorded at poll. A mismatch throws the commands away and queues
// a fresh workflow task. The task row is locked before the workflow row.
func (s *Store) CompleteWorkflowTask(ctx context.Context, taskID, leaseToken int64, commands []WorkflowCommand) error {
	if err := checkCommands(commands); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete workflow task: %w", err)
	}
	defer tx.Rollback(ctx)

	task, err := lockLeasedTask(ctx, tx, taskID, leaseToken)
	if err != nil {
		return err
	}
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
		if err := enqueueWorkflowTask(ctx, tx, task.WorkflowID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit stale workflow task: %w", err)
		}
		return nil
	}

	for _, cmd := range commands {
		if err := applyWorkflowCommand(ctx, tx, task.WorkflowID, cmd); err != nil {
			return err
		}
	}
	if err := markTaskDone(ctx, tx, taskID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workflow task: %w", err)
	}
	return nil
}

// CompleteActivityTask records ActivityCompleted once, then queues a workflow
// task when one is not already open. A second result for the same command is
// ignored and the task is marked done.
func (s *Store) CompleteActivityTask(ctx context.Context, taskID, leaseToken int64, result json.RawMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete activity: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := finishActivity(ctx, tx, taskID, leaseToken, false, result, "", false); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit activity completion: %w", err)
	}
	return nil
}

// FailActivityTask records a terminal ActivityFailed. Retries arrive in a later phase.
func (s *Store) FailActivityTask(ctx context.Context, taskID, leaseToken int64, message string, nonRetryable bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail activity: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := finishActivity(ctx, tx, taskID, leaseToken, true, nil, message, nonRetryable); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit activity failure: %w", err)
	}
	return nil
}

// FailWorkflowTask closes the workflow. nonDeterminism is stored on the event
// so a code mismatch can be told apart from a panic in the workflow function.
func (s *Store) FailWorkflowTask(ctx context.Context, taskID, leaseToken int64, message string, nonDeterminism bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail workflow task: %w", err)
	}
	defer tx.Rollback(ctx)

	task, err := lockLeasedTask(ctx, tx, taskID, leaseToken)
	if err != nil {
		return err
	}
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
	if err := markTaskDone(ctx, tx, taskID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workflow failure: %w", err)
	}
	return nil
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
			return fmt.Errorf("%w: StartTimer", ErrUnsupportedCommand)
		default:
			return fmt.Errorf("%w: %s", ErrUnsupportedCommand, cmd.Type)
		}
	}
	return nil
}

func finishActivity(ctx context.Context, tx pgx.Tx, taskID, leaseToken int64, failed bool, result json.RawMessage, message string, nonRetryable bool) error {
	task, err := lockLeasedTask(ctx, tx, taskID, leaseToken)
	if err != nil {
		return err
	}
	if task.Kind != "activity" || task.CommandSeq == nil {
		return fmt.Errorf("%w: task %d is not an activity task", ErrBadCommand, taskID)
	}
	state, err := lockWorkflow(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}
	done, err := activityTerminal(ctx, tx, task.WorkflowID, *task.CommandSeq)
	if err != nil {
		return err
	}
	if !done {
		event, err := activityResultEvent(*task.CommandSeq, failed, result, message, nonRetryable)
		if err != nil {
			return err
		}
		if err := appendEvents(ctx, tx, task.WorkflowID, event); err != nil {
			return err
		}
		if state.Status == "running" {
			if err := enqueueWorkflowTask(ctx, tx, task.WorkflowID); err != nil {
				return err
			}
		}
	}
	return markTaskDone(ctx, tx, taskID)
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
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedCommand, cmd.Type)
	}
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

type leasedTask struct {
	WorkflowID string
	Kind       string
	CommandSeq *int
	Dispatched *int
}

func lockLeasedTask(ctx context.Context, tx pgx.Tx, taskID, leaseToken int64) (leasedTask, error) {
	var task leasedTask
	err := tx.QueryRow(ctx, `
		SELECT workflow_id, kind, command_seq, history_len_at_dispatch
		FROM tasks
		WHERE id = $1 AND status = 'leased' AND lease_token = $2
		FOR UPDATE
	`, taskID, leaseToken).Scan(&task.WorkflowID, &task.Kind, &task.CommandSeq, &task.Dispatched)
	if errors.Is(err, pgx.ErrNoRows) {
		return leasedTask{}, ErrLeaseMismatch
	}
	if err != nil {
		return leasedTask{}, fmt.Errorf("lock task: %w", err)
	}
	return task, nil
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
