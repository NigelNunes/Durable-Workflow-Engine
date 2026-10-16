// Command bench-worker runs trivial and order workflows for Phase 12 benches.
// When BENCH_METRICS is set, it appends dispatch latency samples (pending→leased).
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nigelnunes/durable-flow/examples/orders"
	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/worker"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

func main() {
	engineURL := os.Getenv("ENGINE_URL")
	demoKey := os.Getenv("DEMO_KEY")
	if engineURL == "" || demoKey == "" {
		fmt.Fprintln(os.Stderr, "ENGINE_URL and DEMO_KEY are required")
		os.Exit(2)
	}
	name := os.Getenv("WORKER_NAME")
	if name == "" {
		name = "bench-worker"
	}

	var metrics *metricsWriter
	if path := os.Getenv("BENCH_METRICS"); path != "" {
		dbURL := os.Getenv("DATABASE_URL")
		if dbURL == "" {
			fmt.Fprintln(os.Stderr, "DATABASE_URL is required when BENCH_METRICS is set")
			os.Exit(2)
		}
		pool, err := pgxpool.New(context.Background(), dbURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer pool.Close()
		metrics = &metricsWriter{path: path, pool: pool}
	}

	w := worker.New(worker.Options{
		Name:      name,
		EngineURL: engineURL,
		DemoKey:   demoKey,
		BeforeWorkflowReplay: func(workflowID string) {
			if metrics != nil {
				metrics.recordWorkflowDispatch(workflowID)
			}
		},
	})

	worker.RegisterWorkflow(w, "bench_noop", noopWorkflow)
	worker.RegisterActivity(w, "NoopA", wrapNoop(metrics, slowActivityDelay()))
	worker.RegisterActivity(w, "NoopB", wrapNoop(metrics, 0))

	if os.Getenv("BENCH_ORDERS") == "1" {
		if os.Getenv("SERVICES_URL") == "" {
			fmt.Fprintln(os.Stderr, "SERVICES_URL is required when BENCH_ORDERS=1")
			os.Exit(2)
		}
		worker.RegisterWorkflow(w, "order_fulfilment", orders.Workflow)
		worker.RegisterActivity(w, "ChargeCard", wrapOrder(metrics, orders.ChargeCard))
		worker.RegisterActivity(w, "ReserveStock", wrapOrder(metrics, orders.ReserveStock))
		worker.RegisterActivity(w, "SendReceipt", wrapOrder(metrics, orders.SendReceipt))
	}

	if err := w.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func noopWorkflow(ctx workflow.Context, _ struct{}) (string, error) {
	if _, err := workflow.Activity[struct{}, string](ctx, "NoopA", struct{}{}, workflow.ActivityOptions{}); err != nil {
		return "", err
	}
	return workflow.Activity[struct{}, string](ctx, "NoopB", struct{}{}, workflow.ActivityOptions{})
}

func noopActivity(_ context.Context, _ struct{}) (string, error) {
	return "ok", nil
}

func slowActivityDelay() time.Duration {
	raw := os.Getenv("BENCH_SLOW_ACTIVITY_MS")
	if raw == "" {
		return 0
	}
	ms, err := time.ParseDuration(raw + "ms")
	if err != nil {
		return 0
	}
	return ms
}

type metricsWriter struct {
	path string
	pool *pgxpool.Pool
	mu   sync.Mutex
}

func (m *metricsWriter) recordWorkflowDispatch(workflowID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var created time.Time
	err := m.pool.QueryRow(ctx, `
		SELECT created_at FROM tasks
		WHERE workflow_id = $1 AND kind = 'workflow' AND status = 'leased'
		ORDER BY id DESC LIMIT 1
	`, workflowID).Scan(&created)
	if err != nil {
		return
	}
	m.append(time.Since(created))
}

func (m *metricsWriter) recordActivityDispatch(info activity.Info) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var created time.Time
	err := m.pool.QueryRow(ctx, `
		SELECT created_at FROM tasks
		WHERE workflow_id = $1 AND kind = 'activity' AND command_seq = $2 AND status = 'leased'
		ORDER BY id DESC LIMIT 1
	`, info.WorkflowID, info.CommandSeq).Scan(&created)
	if err != nil {
		return
	}
	m.append(time.Since(created))
}

func (m *metricsWriter) append(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(m.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%d\n", d.Microseconds())
}

func wrapNoop(m *metricsWriter, delay time.Duration) func(context.Context, struct{}) (string, error) {
	return func(ctx context.Context, in struct{}) (string, error) {
		if m != nil {
			m.recordActivityDispatch(activity.GetInfo(ctx))
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-timer.C:
			}
		}
		return noopActivity(ctx, in)
	}
}

func wrapOrder[In, Out any](m *metricsWriter, fn func(context.Context, In) (Out, error)) func(context.Context, In) (Out, error) {
	return func(ctx context.Context, in In) (Out, error) {
		if m != nil {
			m.recordActivityDispatch(activity.GetInfo(ctx))
		}
		return fn(ctx, in)
	}
}
