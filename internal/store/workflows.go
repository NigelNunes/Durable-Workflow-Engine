package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nigelnunes/durable-flow/internal/history"
)

// Workflow is one persisted workflow plus fields derived from its history and tasks.
type Workflow struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result"`
	Error       *string         `json:"error"`
	HistoryLen  int             `json:"history_len"`
	CurrentStep string          `json:"current_step"`
	OwnerWorker string          `json:"owner_worker"`
	Resumed     bool            `json:"resumed"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Event is one row of a workflow's append-only history.
type Event struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListFilter selects a page of workflows, newest first.
type ListFilter struct {
	Status string
	Type   string
	Limit  int
	Before *time.Time
}

// NewEvent is a history row that does not yet have a sequence number.
type NewEvent struct {
	Type string
	Data json.RawMessage
}

// StartWorkflow inserts a running workflow, its WorkflowStarted event, and one
// pending workflow task, all in a single transaction. If id already exists,
// that id is returned and nothing is written. An empty id is generated.
func (s *Store) StartWorkflow(ctx context.Context, id, workflowType string, input json.RawMessage) (string, error) {
	if workflowType == "" {
		return "", fmt.Errorf("workflow type is required")
	}
	if len(input) == 0 {
		input = json.RawMessage("null")
	}

	supplied := id != ""
	for attempt := 0; attempt < 5; attempt++ {
		if !supplied {
			var err error
			id, err = newWorkflowID()
			if err != nil {
				return "", err
			}
		}
		created, err := s.insertNewWorkflow(ctx, id, workflowType, input)
		if err != nil {
			return "", err
		}
		if created || supplied {
			return id, nil
		}
	}
	return "", fmt.Errorf("generate workflow id: too many collisions")
}

func (s *Store) insertNewWorkflow(ctx context.Context, id, workflowType string, input json.RawMessage) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin start workflow: %w", err)
	}
	defer tx.Rollback(ctx)

	var inserted string
	err = tx.QueryRow(ctx, `
		INSERT INTO workflows (id, type, status, input)
		VALUES ($1, $2, 'running', $3::jsonb)
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`, id, workflowType, string(input)).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert workflow: %w", err)
	}

	data, err := json.Marshal(struct {
		Type  string          `json:"type"`
		Input json.RawMessage `json:"input"`
	}{Type: workflowType, Input: input})
	if err != nil {
		return false, fmt.Errorf("encode workflow started: %w", err)
	}
	if err := appendEvents(ctx, tx, id, NewEvent{Type: history.WorkflowStarted, Data: data}); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (workflow_id, kind, status)
		VALUES ($1, 'workflow', 'pending')
	`, id); err != nil {
		return false, fmt.Errorf("insert workflow task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit start workflow: %w", err)
	}
	return true, nil
}

// appendEvents locks the workflow row, then assigns contiguous seq numbers
// starting at history_len+1.
func appendEvents(ctx context.Context, tx pgx.Tx, workflowID string, events ...NewEvent) error {
	if len(events) == 0 {
		return nil
	}

	var historyLen int
	err := tx.QueryRow(ctx, `
		SELECT history_len FROM workflows WHERE id = $1 FOR UPDATE
	`, workflowID).Scan(&historyLen)
	if err != nil {
		return fmt.Errorf("lock workflow: %w", err)
	}

	for _, event := range events {
		historyLen++
		data := event.Data
		if len(data) == 0 {
			data = json.RawMessage("{}")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workflow_events (workflow_id, seq, type, data)
			VALUES ($1, $2, $3, $4::jsonb)
		`, workflowID, historyLen, event.Type, string(data)); err != nil {
			return fmt.Errorf("insert event %s: %w", event.Type, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE workflows SET history_len = $2, updated_at = now() WHERE id = $1
	`, workflowID, historyLen); err != nil {
		return fmt.Errorf("update history_len: %w", err)
	}
	return nil
}

// GetWorkflow returns one workflow, or ErrNotFound.
func (s *Store) GetWorkflow(ctx context.Context, id string) (Workflow, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+workflowColumns+` FROM workflows w WHERE w.id = $1`, id)
	w, err := scanWorkflow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workflow{}, ErrNotFound
	}
	if err != nil {
		return Workflow{}, fmt.Errorf("get workflow: %w", err)
	}
	return w, nil
}

// ListWorkflows returns workflows newest first.
func (s *Store) ListWorkflows(ctx context.Context, f ListFilter) ([]Workflow, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows w
		WHERE ($1 = '' OR w.status = $1)
		  AND ($2 = '' OR w.type = $2)
		  AND ($3::timestamptz IS NULL OR w.created_at < $3)
		ORDER BY w.created_at DESC, w.id DESC
		LIMIT $4
	`, f.Status, f.Type, f.Before, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	out := []Workflow{}
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, fmt.Errorf("list workflows: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	return out, nil
}

// GetEvents returns the history of one workflow ordered by seq, or ErrNotFound.
func (s *Store) GetEvents(ctx context.Context, workflowID string) ([]Event, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1)`, workflowID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check workflow: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT seq, type, data::text, created_at
		FROM workflow_events
		WHERE workflow_id = $1
		ORDER BY seq
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var (
			event Event
			data  string
		)
		if err := rows.Scan(&event.Seq, &event.Type, &data, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("get events: %w", err)
		}
		event.Data = json.RawMessage(data)
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	return out, nil
}

func newWorkflowID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate workflow id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// workflowColumns includes derived fields. current_step is the latest scheduled
// activity name. owner_worker is whoever holds a leased task. resumed is true
// when a lease-expiry event has been recorded.
var workflowColumns = fmt.Sprintf(`
	w.id, w.type, w.status, w.input::text, w.result::text, w.error, w.history_len,
	COALESCE((
		SELECT e.data->>'name' FROM workflow_events e
		WHERE e.workflow_id = w.id AND e.type = '%s'
		ORDER BY e.seq DESC LIMIT 1
	), ''),
	COALESCE((
		SELECT t.lease_owner FROM tasks t
		WHERE t.workflow_id = w.id AND t.status = 'leased' AND t.lease_owner IS NOT NULL
		ORDER BY t.updated_at DESC LIMIT 1
	), ''),
	EXISTS (
		SELECT 1 FROM workflow_events e
		WHERE e.workflow_id = w.id AND e.type IN ('%s', '%s')
	),
	w.created_at, w.updated_at
`, history.ActivityScheduled, history.ActivityLeaseExpired, history.WorkflowTaskLeaseExpired)

func scanWorkflow(row pgx.Row) (Workflow, error) {
	var (
		w      Workflow
		input  string
		result *string
	)
	err := row.Scan(
		&w.ID, &w.Type, &w.Status, &input, &result, &w.Error, &w.HistoryLen,
		&w.CurrentStep, &w.OwnerWorker, &w.Resumed, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return Workflow{}, err
	}
	w.Input = json.RawMessage(input)
	if result != nil {
		w.Result = json.RawMessage(*result)
	}
	return w, nil
}
