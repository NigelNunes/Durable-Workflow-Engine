package store

import (
	"context"
	"fmt"
	"time"

	"github.com/nigelnunes/durable-flow/internal/history"
)

// WorkerView is one registered worker plus how many tasks it currently holds.
type WorkerView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Alive         bool      `json:"alive"`
	RunningTasks  int       `json:"running_tasks"`
}

// DashboardWorkflow is one workflow row shaped for the dashboard.
type DashboardWorkflow struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	CurrentStep string    `json:"current_step"`
	StepIndex   int       `json:"step_index"`
	TotalSteps  int       `json:"total_steps"`
	OwnerWorker string    `json:"owner_worker"`
	Resumed     bool      `json:"resumed"`
	CreatedAt   time.Time `json:"created_at"`
}

// DashboardCounters are whole-table totals, not a page of workflows.
type DashboardCounters struct {
	Running                  int `json:"running"`
	Completed                int `json:"completed"`
	Failed                   int `json:"failed"`
	RecoveredAfterCrash      int `json:"recovered_after_crash"`
	StaleCompletionsRejected int `json:"stale_completions_rejected"`
	AvgRecoveryMS            int `json:"avg_recovery_ms"`
}

// ListWorkers returns every worker. aliveFor is how long a heartbeat stays fresh.
func (s *Store) ListWorkers(ctx context.Context, aliveFor time.Duration) ([]WorkerView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.name, w.last_heartbeat,
			w.last_heartbeat > now() - ($1::bigint * interval '1 millisecond'),
			(SELECT count(*) FROM tasks t WHERE t.status = 'leased' AND t.lease_owner = w.id)
		FROM workers w
		ORDER BY w.name, w.id
	`, aliveFor.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()
	var out []WorkerView
	for rows.Next() {
		var w WorkerView
		if err := rows.Scan(&w.ID, &w.Name, &w.LastHeartbeat, &w.Alive, &w.RunningTasks); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	if out == nil {
		out = []WorkerView{}
	}
	return out, nil
}

// DashboardWorkflows returns the most recently updated workflows.
func (s *Store) DashboardWorkflows(ctx context.Context, limit int) ([]DashboardWorkflow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.type, w.status,
			COALESCE((
				SELECT e.data->>'name' FROM workflow_events e
				WHERE e.workflow_id = w.id AND e.type = $1
				ORDER BY e.seq DESC LIMIT 1
			), ''),
			(
				SELECT count(*) FROM workflow_events e
				WHERE e.workflow_id = w.id AND e.type = $1
			),
			COALESCE((
				SELECT wr.name FROM tasks t
				JOIN workers wr ON wr.id = t.lease_owner
				WHERE t.workflow_id = w.id AND t.status = 'leased' AND t.lease_owner IS NOT NULL
				ORDER BY t.updated_at DESC LIMIT 1
			), ''),
			EXISTS (
				SELECT 1 FROM workflow_events e
				WHERE e.workflow_id = w.id AND e.type IN ($2, $3)
			),
			w.created_at
		FROM workflows w
		ORDER BY w.updated_at DESC
		LIMIT $4
	`, history.ActivityScheduled, history.ActivityLeaseExpired, history.WorkflowTaskLeaseExpired, limit)
	if err != nil {
		return nil, fmt.Errorf("list dashboard workflows: %w", err)
	}
	defer rows.Close()
	var out []DashboardWorkflow
	for rows.Next() {
		var w DashboardWorkflow
		var scheduled int
		if err := rows.Scan(&w.ID, &w.Type, &w.Status, &w.CurrentStep, &scheduled, &w.OwnerWorker, &w.Resumed, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan dashboard workflow: %w", err)
		}
		if scheduled > 0 {
			w.StepIndex = scheduled - 1
		}
		if w.Type == "order_fulfilment" {
			w.TotalSteps = 3
		} else {
			w.TotalSteps = scheduled
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list dashboard workflows: %w", err)
	}
	if out == nil {
		out = []DashboardWorkflow{}
	}
	return out, nil
}

// DashboardCounters aggregates the workflow table and its history.
func (s *Store) DashboardCounters(ctx context.Context) (DashboardCounters, error) {
	var c DashboardCounters
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'running'),
			count(*) FILTER (WHERE status = 'completed'),
			count(*) FILTER (WHERE status = 'failed')
		FROM workflows
	`).Scan(&c.Running, &c.Completed, &c.Failed); err != nil {
		return DashboardCounters{}, fmt.Errorf("count workflow statuses: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM workflows w
		WHERE w.status = 'completed'
		AND EXISTS (
			SELECT 1 FROM workflow_events e
			WHERE e.workflow_id = w.id AND e.type IN ($1, $2)
		)
	`, history.ActivityLeaseExpired, history.WorkflowTaskLeaseExpired).Scan(&c.RecoveredAfterCrash); err != nil {
		return DashboardCounters{}, fmt.Errorf("count recovered workflows: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_events WHERE type = $1
	`, history.StaleCompletionRejected).Scan(&c.StaleCompletionsRejected); err != nil {
		return DashboardCounters{}, fmt.Errorf("count stale completions: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(round(avg(EXTRACT(EPOCH FROM (done.created_at - exp.created_at)) * 1000)), 0)::int
		FROM workflow_events exp
		JOIN LATERAL (
			SELECT created_at FROM workflow_events done
			WHERE done.workflow_id = exp.workflow_id
			  AND done.seq > exp.seq
			  AND done.type IN ($1, $2)
			ORDER BY done.seq
			LIMIT 1
		) done ON true
		WHERE exp.type IN ($3, $4)
	`, history.ActivityCompleted, history.WorkflowCompleted, history.ActivityLeaseExpired, history.WorkflowTaskLeaseExpired).Scan(&c.AvgRecoveryMS); err != nil {
		return DashboardCounters{}, fmt.Errorf("average recovery: %w", err)
	}
	return c, nil
}
