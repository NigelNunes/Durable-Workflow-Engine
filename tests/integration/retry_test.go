package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/client"
	"github.com/nigelnunes/durable-flow/sdk/worker"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

func TestActivityRetriesThenSucceeds(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 8, time.Second)
	var mu sync.Mutex
	var starts []time.Time
	var attempts []int

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := worker.New(worker.Options{Name: "retry-worker", EngineURL: srv.URL, DemoKey: demoKey})
	w.RegisterWorkflow("retry_flaky", func(ctx workflow.Context, _ struct{}) (int, error) {
		return workflow.Activity[struct{}, int](ctx, "Flaky", struct{}{}, workflow.ActivityOptions{
			Retry: workflow.RetryPolicy{
				MaxAttempts:        4,
				InitialInterval:    200 * time.Millisecond,
				BackoffCoefficient: 2,
				MaxInterval:        time.Second,
			},
		})
	})
	w.RegisterActivity("Flaky", func(ctx context.Context, _ struct{}) (int, error) {
		info := activity.GetInfo(ctx)
		mu.Lock()
		starts = append(starts, time.Now())
		attempts = append(attempts, info.Attempt)
		mu.Unlock()
		if info.Attempt < 4 {
			return 0, errors.New("not yet")
		}
		return 10, nil
	})
	errCh := make(chan error, 1)
	go func() {
		err := w.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "retry_flaky", struct{}{}, "")
	if err != nil {
		t.Fatal(err)
	}
	wf := waitWorkflow(t, c, id)
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	if wf.Status != "completed" {
		t.Fatalf("status %s error %v", wf.Status, wf.Error)
	}
	var result int
	if err := json.Unmarshal(wf.Result, &result); err != nil || result != 10 {
		t.Fatalf("result %s", wf.Result)
	}
	if fmt.Sprint(attempts) != "[1 2 3 4]" {
		t.Fatalf("attempts %v", attempts)
	}

	bases := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	for i, base := range bases {
		gap := starts[i+1].Sub(starts[i])
		upper := time.Duration(float64(base)*1.2) + 150*time.Millisecond
		if gap < base || gap > upper {
			t.Fatalf("attempt gap %d = %s, want %s to %s", i+1, gap, base, upper)
		}
	}

	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var retries, completed int
	for i, ev := range events {
		switch ev.Type {
		case "ActivityRetryScheduled":
			retries++
			var body struct {
				Attempt   int       `json:"attempt"`
				NextRunAt time.Time `json:"next_run_at"`
			}
			if err := json.Unmarshal(ev.Data, &body); err != nil {
				t.Fatal(err)
			}
			base := bases[retries-1]
			scheduled := body.NextRunAt.Sub(ev.CreatedAt)
			if scheduled < base-25*time.Millisecond || scheduled > time.Duration(float64(base)*1.2)+25*time.Millisecond {
				t.Fatalf("retry %d scheduled %s from %s, base %s", retries, scheduled, ev.CreatedAt, base)
			}
			if body.Attempt != retries+1 {
				t.Fatalf("retry event attempt %d, want %d", body.Attempt, retries+1)
			}
		case "ActivityCompleted":
			completed++
		case "ActivityFailed":
			t.Fatalf("terminal failure in history event %d", i)
		}
	}
	if retries != 3 || completed != 1 {
		t.Fatalf("retries %d completed %d", retries, completed)
	}
}

func TestNonRetryableActivityFailsOnce(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 4, time.Second)
	var calls int
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := worker.New(worker.Options{Name: "once-worker", EngineURL: srv.URL, DemoKey: demoKey})
	w.RegisterWorkflow("retry_once", func(ctx workflow.Context, _ struct{}) (int, error) {
		return workflow.Activity[struct{}, int](ctx, "Nope", struct{}{}, workflow.ActivityOptions{
			Retry: workflow.RetryPolicy{
				MaxAttempts:        5,
				InitialInterval:    time.Second,
				BackoffCoefficient: 2,
				MaxInterval:        time.Minute,
			},
		})
	})
	w.RegisterActivity("Nope", func(context.Context, struct{}) (int, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return 0, workflow.NonRetryable(errors.New("nope"))
	})
	go func() { _ = w.Run(ctx) }()

	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "retry_once", struct{}{}, "")
	if err != nil {
		t.Fatal(err)
	}
	wf := waitWorkflow(t, c, id)
	if wf.Status != "failed" || wf.Error == nil || *wf.Error != "nope" {
		t.Fatalf("workflow %+v", wf)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("activity calls %d", gotCalls)
	}
	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var retries, failed int
	for _, ev := range events {
		switch ev.Type {
		case "ActivityRetryScheduled":
			retries++
		case "ActivityFailed":
			failed++
		}
	}
	if retries != 0 || failed != 1 {
		t.Fatalf("retries %d failed %d types %v", retries, failed, eventTypes(events))
	}
}

func TestStaleActivityCompletion(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 4, 50*time.Millisecond)
	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "hold", struct{}{}, "stale-act")
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 5 * time.Second}
	workerA := registerWorker(t, httpClient, srv.URL, "worker-a")
	wfTask := pollWorkflow(t, httpClient, srv.URL, workerA)
	res := postJSON(t, httpClient, fmt.Sprintf("%s/v1/tasks/%d/complete-workflow", srv.URL, wfTask.TaskID), fmt.Sprintf(`{
		"lease_token": %d,
		"worker_id": %q,
		"commands": [{"type":"ScheduleActivity","seq":1,"name":"Hold","input":{}}]
	}`, wfTask.LeaseToken, workerA))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("schedule status %d: %s", res.StatusCode, res.Body)
	}

	status, taskA, err := pollActivity(httpClient, srv.URL, workerA, 2000)
	if err != nil || status != http.StatusOK {
		t.Fatalf("poll A status %d err %v", status, err)
	}
	rewindLease(t, taskA.TaskID)
	waitTaskStatus(t, taskA.TaskID, "pending")

	workerB := registerWorker(t, httpClient, srv.URL, "worker-b")
	status, taskB, err := pollActivity(httpClient, srv.URL, workerB, 2000)
	if err != nil || status != http.StatusOK {
		t.Fatalf("poll B status %d err %v", status, err)
	}
	res = postJSON(t, httpClient, fmt.Sprintf("%s/v1/tasks/%d/complete-activity", srv.URL, taskB.TaskID), fmt.Sprintf(
		`{"lease_token":%d,"worker_id":%q,"result":"from-b"}`, taskB.LeaseToken, workerB,
	))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("complete B status %d: %s", res.StatusCode, res.Body)
	}
	res = postJSON(t, httpClient, fmt.Sprintf("%s/v1/tasks/%d/complete-activity", srv.URL, taskA.TaskID), fmt.Sprintf(
		`{"lease_token":%d,"worker_id":%q,"result":"from-a"}`, taskA.LeaseToken, workerA,
	))
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale complete status %d: %s", res.StatusCode, res.Body)
	}

	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var completed int
	var staleWorker string
	for _, ev := range events {
		switch ev.Type {
		case "ActivityCompleted":
			completed++
			var body struct {
				Result string `json:"result"`
			}
			if err := json.Unmarshal(ev.Data, &body); err != nil {
				t.Fatal(err)
			}
			if body.Result != "from-b" {
				t.Fatalf("completed result %q", body.Result)
			}
		case "StaleCompletionRejected":
			var body struct {
				WorkerID string `json:"worker_id"`
			}
			if err := json.Unmarshal(ev.Data, &body); err != nil {
				t.Fatal(err)
			}
			staleWorker = body.WorkerID
		}
	}
	if completed != 1 || staleWorker != workerA {
		t.Fatalf("completed %d stale worker %q types %v", completed, staleWorker, eventTypes(events))
	}
}

func TestRedeliveryCapFailsActivity(t *testing.T) {
	srv := startQueueServer(t, 10*time.Second, 4, 50*time.Millisecond, 2)
	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "cap_wf", struct{}{}, "cap-wf")
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, httpClient, srv.URL, "cap-worker")
	wfTask := pollWorkflow(t, httpClient, srv.URL, workerID)
	res := postJSON(t, httpClient, fmt.Sprintf("%s/v1/tasks/%d/complete-workflow", srv.URL, wfTask.TaskID), fmt.Sprintf(`{
		"lease_token": %d,
		"worker_id": %q,
		"commands": [{"type":"ScheduleActivity","seq":1,"name":"Hold","input":{}}]
	}`, wfTask.LeaseToken, workerID))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("schedule status %d: %s", res.StatusCode, res.Body)
	}

	var taskID int64
	for i := 0; i < 3; i++ {
		status, task, err := pollActivity(httpClient, srv.URL, workerID, 2000)
		if err != nil || status != http.StatusOK {
			t.Fatalf("poll %d status %d err %v", i, status, err)
		}
		taskID = task.TaskID
		rewindLease(t, taskID)
		want := "pending"
		if i == 2 {
			want = "done"
		}
		waitTaskStatus(t, taskID, want)
	}

	var open int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM tasks
		WHERE workflow_id = $1 AND kind = 'activity' AND status IN ('pending', 'leased')
	`, id).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Fatalf("open activity tasks %d", open)
	}
	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var failed bool
	for _, ev := range events {
		if ev.Type != "ActivityFailed" {
			continue
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(ev.Data, &body); err != nil {
			t.Fatal(err)
		}
		failed = body.Error == "TooManyRedeliveries"
	}
	if !failed {
		t.Fatalf("history %v", eventTypes(events))
	}
}

func waitTaskStatus(t *testing.T, taskID int64, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %d status %s, want %s", taskID, got, status)
}
