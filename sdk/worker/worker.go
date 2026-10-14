// Package worker polls the engine, replays workflow code, and runs activities.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

// Options configures a worker process.
type Options struct {
	Name                    string
	EngineURL               string
	DemoKey                 string
	MaxConcurrentActivities int
	// PauseHeartbeats, when non-nil and true, stops worker and activity
	// heartbeats and stops the poll loop from leasing more tasks.
	// The activity already running can still finish.
	// Chaos tests use it so a live process can lose its lease.
	// Normal workers leave it nil.
	PauseHeartbeats func() bool
	// BeforeWorkflowReplay runs after a workflow task is leased and before replay.
	// Chaos tests block here so the process can be killed mid task.
	// Normal workers leave it nil.
	BeforeWorkflowReplay func(workflowID string)
}

// Worker executes registered workflows and activities.
type Worker struct {
	name                 string
	maxActivities        int
	http                 *engineClient
	workflows            map[string]workflowFunc
	activities           map[string]activityFunc
	workerID             string
	heartbeatEvery       time.Duration
	pauseHeartbeats      func() bool
	beforeWorkflowReplay func(string)
	exit                 func(int)
	internalPause        atomic.Bool
	stallMu              sync.Mutex
	stallUntil           time.Time
}

type polledTask struct {
	TaskID         int64                   `json:"task_id"`
	Kind           string                  `json:"kind"`
	LeaseToken     int64                   `json:"lease_token"`
	Workflow       *polledWorkflow         `json:"workflow"`
	History        []workflow.HistoryEvent `json:"history"`
	WorkflowID     string                  `json:"workflow_id"`
	CommandSeq     int                     `json:"command_seq"`
	ActivityName   string                  `json:"activity_name"`
	Input          json.RawMessage         `json:"input"`
	Attempt        int                     `json:"attempt"`
	IdempotencyKey string                  `json:"idempotency_key"`
}

type polledWorkflow struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Input json.RawMessage `json:"input"`
}

type registerResponse struct {
	WorkerID            string `json:"worker_id"`
	HeartbeatIntervalMS int64  `json:"heartbeat_interval_ms"`
}

// New returns a worker that talks to the engine over HTTP.
// MaxConcurrentActivities defaults to 10.
func New(opts Options) *Worker {
	if opts.MaxConcurrentActivities <= 0 {
		opts.MaxConcurrentActivities = 10
	}
	if opts.Name == "" {
		opts.Name = "worker"
	}
	return &Worker{
		name:          opts.Name,
		maxActivities: opts.MaxConcurrentActivities,
		http: &engineClient{
			base: trimSlash(opts.EngineURL),
			key:  opts.DemoKey,
			http: &http.Client{},
		},
		workflows:            map[string]workflowFunc{},
		activities:           map[string]activityFunc{},
		pauseHeartbeats:      opts.PauseHeartbeats,
		beforeWorkflowReplay: opts.BeforeWorkflowReplay,
		exit:                 os.Exit,
	}
}

func (w *Worker) heartbeatsPaused() bool {
	if w.internalPause.Load() {
		return true
	}
	return w.pauseHeartbeats != nil && w.pauseHeartbeats()
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// RegisterWorkflow registers fn under name.
// fn must be func(workflow.Context, In) (Out, error).
func (w *Worker) RegisterWorkflow(name string, fn any) {
	if name == "" {
		panic("workflow name is required")
	}
	adapted, err := adaptWorkflow(fn)
	if err != nil {
		panic(err)
	}
	w.workflows[name] = adapted
}

// RegisterActivity registers fn under name.
// fn must be func(context.Context, In) (Out, error).
func (w *Worker) RegisterActivity(name string, fn any) {
	if name == "" {
		panic("activity name is required")
	}
	adapted, err := adaptActivity(fn)
	if err != nil {
		panic(err)
	}
	w.activities[name] = adapted
}

// RegisterWorkflow is the typed form of Worker.RegisterWorkflow.
func RegisterWorkflow[In, Out any](w *Worker, name string, fn func(workflow.Context, In) (Out, error)) {
	w.RegisterWorkflow(name, fn)
}

// RegisterActivity is the typed form of Worker.RegisterActivity.
func RegisterActivity[In, Out any](w *Worker, name string, fn func(context.Context, In) (Out, error)) {
	w.RegisterActivity(name, fn)
}

// Run registers this process, then polls until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	var reg registerResponse
	status, err := w.http.post(ctx, "/v1/workers/register", map[string]string{"name": w.name}, &reg)
	if err != nil {
		return fmt.Errorf("register worker: %w", err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("register worker: http %d", status)
	}
	w.workerID = reg.WorkerID
	w.heartbeatEvery = time.Duration(reg.HeartbeatIntervalMS) * time.Millisecond
	if w.heartbeatEvery <= 0 {
		w.heartbeatEvery = 3 * time.Second
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.heartbeatWorker(runCtx)

	sem := make(chan struct{}, w.maxActivities)
	var wg sync.WaitGroup
	for {
		if runCtx.Err() != nil {
			break
		}
		kinds := []string{"workflow", "activity"}
		if len(sem) >= w.maxActivities {
			kinds = []string{"workflow"}
		}
		if w.heartbeatsPaused() {
			if !sleep(runCtx, 50*time.Millisecond) {
				break
			}
			continue
		}
		task, ok, err := w.poll(runCtx, kinds)
		if err != nil {
			if runCtx.Err() != nil {
				break
			}
			slog.Error("poll tasks", "err", err)
			if !sleep(runCtx, 200*time.Millisecond) {
				break
			}
			continue
		}
		if !ok {
			continue
		}
		if task.Kind == "workflow" {
			w.handleWorkflow(runCtx, task)
			continue
		}
		select {
		case <-runCtx.Done():
		case sem <- struct{}{}:
			wg.Add(1)
			go func(task polledTask) {
				defer wg.Done()
				defer func() { <-sem }()
				w.handleActivity(runCtx, task)
			}(task)
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (w *Worker) heartbeatWorker(ctx context.Context) {
	ticker := time.NewTicker(w.heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.heartbeatsPaused() {
				continue
			}
			var body struct {
				Chaos *string `json:"chaos"`
			}
			if _, err := w.http.post(ctx, "/v1/workers/"+w.workerID+"/heartbeat", map[string]any{}, &body); err != nil {
				continue
			}
			if body.Chaos != nil && *body.Chaos != "" {
				w.applyChaos(*body.Chaos)
			}
		}
	}
}

func (w *Worker) poll(ctx context.Context, kinds []string) (polledTask, bool, error) {
	wait := 20000
	var task polledTask
	status, err := w.http.post(ctx, "/v1/tasks/poll", map[string]any{
		"worker_id": w.workerID,
		"kinds":     kinds,
		"wait_ms":   wait,
	}, &task)
	if err != nil {
		return polledTask{}, false, err
	}
	if status == http.StatusNoContent {
		return polledTask{}, false, nil
	}
	return task, true, nil
}

func (w *Worker) handleWorkflow(ctx context.Context, task polledTask) {
	if task.Workflow == nil {
		w.failWorkflow(ctx, task, "workflow task missing workflow", false)
		return
	}
	fn, ok := w.workflows[task.Workflow.Type]
	if !ok {
		w.failWorkflow(ctx, task, "workflow not registered: "+task.Workflow.Type, false)
		return
	}
	if w.beforeWorkflowReplay != nil {
		w.beforeWorkflowReplay(task.Workflow.ID)
	}
	commands, failure := fn(task.Workflow.ID, task.Workflow.Input, task.History)
	if failure != nil {
		w.failWorkflow(ctx, task, failure.Message, failure.NonDeterminism)
		return
	}
	body, err := workflowCompletion(task.LeaseToken, w.workerID, commands)
	if err != nil {
		w.failWorkflow(ctx, task, err.Error(), false)
		return
	}
	status, err := w.http.post(ctx, fmt.Sprintf("/v1/tasks/%d/complete-workflow", task.TaskID), body, nil)
	if err == nil || status == http.StatusConflict {
		return
	}
	if status == http.StatusBadRequest {
		w.failWorkflow(ctx, task, err.Error(), false)
		return
	}
	slog.Error("complete workflow task", "task", task.TaskID, "err", err)
}

func (w *Worker) handleActivity(ctx context.Context, task polledTask) {
	fn, ok := w.activities[task.ActivityName]
	if !ok {
		w.failActivity(ctx, task, "activity not registered: "+task.ActivityName, true)
		return
	}
	key := task.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("%s:%d", task.WorkflowID, task.CommandSeq)
	}
	actCtx := activity.WithInfo(ctx, activity.Info{
		WorkflowID:     task.WorkflowID,
		CommandSeq:     task.CommandSeq,
		Attempt:        task.Attempt,
		IdempotencyKey: key,
	})
	actCtx, cancel := context.WithCancel(actCtx)
	defer cancel()

	var leaseLost atomic.Bool
	hbCtx, hbCancel := context.WithCancel(ctx)
	var hb sync.WaitGroup
	hb.Add(1)
	go func() {
		defer hb.Done()
		w.heartbeatActivity(hbCtx, task, &leaseLost, cancel)
	}()

	result, err := fn(actCtx, task.Input)
	hbCancel()
	hb.Wait()
	if leaseLost.Load() {
		return
	}
	w.waitStall()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		nonRetryable := false
		var appErr *workflow.ApplicationError
		if errors.As(err, &appErr) {
			nonRetryable = appErr.NonRetryable
		}
		w.failActivity(ctx, task, err.Error(), nonRetryable)
		return
	}
	status, postErr := w.http.post(ctx, fmt.Sprintf("/v1/tasks/%d/complete-activity", task.TaskID), struct {
		LeaseToken int64           `json:"lease_token"`
		WorkerID   string          `json:"worker_id"`
		Result     json.RawMessage `json:"result"`
	}{LeaseToken: task.LeaseToken, WorkerID: w.workerID, Result: result}, nil)
	if postErr != nil && status != http.StatusConflict {
		slog.Error("complete activity", "task", task.TaskID, "err", postErr)
	}
}

func (w *Worker) heartbeatActivity(ctx context.Context, task polledTask, leaseLost *atomic.Bool, cancel context.CancelFunc) {
	ticker := time.NewTicker(w.heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.heartbeatsPaused() {
				continue
			}
			status, err := w.http.post(ctx, fmt.Sprintf("/v1/tasks/%d/heartbeat", task.TaskID), map[string]any{
				"lease_token": task.LeaseToken,
			}, nil)
			if status == http.StatusConflict {
				leaseLost.Store(true)
				cancel()
				return
			}
			if err != nil && ctx.Err() == nil {
				slog.Error("activity heartbeat", "task", task.TaskID, "err", err)
			}
		}
	}
}

func (w *Worker) failWorkflow(ctx context.Context, task polledTask, message string, nonDeterminism bool) {
	_, err := w.http.post(ctx, fmt.Sprintf("/v1/tasks/%d/fail-workflow", task.TaskID), map[string]any{
		"lease_token":     task.LeaseToken,
		"worker_id":       w.workerID,
		"error":           message,
		"non_determinism": nonDeterminism,
	}, nil)
	if err != nil {
		slog.Error("fail workflow task", "task", task.TaskID, "err", err)
	}
}

func (w *Worker) failActivity(ctx context.Context, task polledTask, message string, nonRetryable bool) {
	_, err := w.http.post(ctx, fmt.Sprintf("/v1/tasks/%d/fail-activity", task.TaskID), map[string]any{
		"lease_token":   task.LeaseToken,
		"worker_id":     w.workerID,
		"error":         message,
		"non_retryable": nonRetryable,
	}, nil)
	if err != nil {
		slog.Error("fail activity", "task", task.TaskID, "err", err)
	}
}

type wireRetry struct {
	MaxAttempts       int     `json:"max_attempts"`
	InitialIntervalMS int64   `json:"initial_interval_ms"`
	Backoff           float64 `json:"backoff"`
	MaxIntervalMS     int64   `json:"max_interval_ms"`
}

func workflowCompletion(token int64, workerID string, commands []workflow.Command) (any, error) {
	wire := make([]any, 0, len(commands))
	for _, cmd := range commands {
		encoded, err := wireCommand(cmd)
		if err != nil {
			return nil, err
		}
		wire = append(wire, encoded)
	}
	return struct {
		LeaseToken int64  `json:"lease_token"`
		WorkerID   string `json:"worker_id"`
		Commands   []any  `json:"commands"`
	}{LeaseToken: token, WorkerID: workerID, Commands: wire}, nil
}

func wireCommand(cmd workflow.Command) (any, error) {
	switch c := cmd.(type) {
	case workflow.ScheduleActivity:
		input := c.Input
		if len(input) == 0 {
			input = json.RawMessage("null")
		}
		return struct {
			Type        string          `json:"type"`
			Seq         int             `json:"seq"`
			Name        string          `json:"name"`
			Input       json.RawMessage `json:"input"`
			RetryPolicy wireRetry       `json:"retry_policy"`
		}{
			Type:        "ScheduleActivity",
			Seq:         c.Seq,
			Name:        c.Name,
			Input:       input,
			RetryPolicy: retryFrom(c.Retry),
		}, nil
	case workflow.CompleteWorkflow:
		result := c.Result
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		return struct {
			Type   string          `json:"type"`
			Result json.RawMessage `json:"result"`
		}{Type: "CompleteWorkflow", Result: result}, nil
	case workflow.FailWorkflow:
		return struct {
			Type  string `json:"type"`
			Error string `json:"error"`
		}{Type: "FailWorkflow", Error: c.Error}, nil
	case workflow.StartTimer:
		return struct {
			Type   string    `json:"type"`
			Seq    int       `json:"seq"`
			FireAt time.Time `json:"fire_at"`
		}{Type: "StartTimer", Seq: c.Seq, FireAt: c.FireAt}, nil
	default:
		return nil, fmt.Errorf("unknown command %T", cmd)
	}
}

func retryFrom(p workflow.RetryPolicy) wireRetry {
	attempts := p.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	return wireRetry{
		MaxAttempts:       attempts,
		InitialIntervalMS: p.InitialInterval.Milliseconds(),
		Backoff:           p.BackoffCoefficient,
		MaxIntervalMS:     p.MaxInterval.Milliseconds(),
	}
}

func (w *Worker) applyChaos(chaos string) {
	if chaos == "crash" {
		w.exit(137)
		return
	}
	secs, ok := stallSeconds(chaos)
	if !ok {
		return
	}
	until := time.Now().Add(time.Duration(secs) * time.Second)
	w.stallMu.Lock()
	if until.After(w.stallUntil) {
		w.stallUntil = until
	}
	w.stallMu.Unlock()
	w.internalPause.Store(true)
	if secs == 0 {
		return
	}
	time.AfterFunc(time.Duration(secs)*time.Second, func() {
		w.internalPause.Store(false)
	})
}

func stallSeconds(chaos string) (int, bool) {
	raw, ok := strings.CutPrefix(chaos, "stall:")
	if !ok {
		return 0, false
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return 0, false
	}
	return secs, true
}

func (w *Worker) waitStall() {
	w.stallMu.Lock()
	until := w.stallUntil
	w.stallMu.Unlock()
	if d := time.Until(until); d > 0 {
		time.Sleep(d)
	}
}
