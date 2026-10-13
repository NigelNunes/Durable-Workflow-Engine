package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/client"
	"github.com/nigelnunes/durable-flow/sdk/worker"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

type pair struct {
	A int `json:"a"`
	B int `json:"b"`
}

func addDouble(ctx workflow.Context, in pair) (int, error) {
	sum, err := workflow.Activity[pair, int](ctx, "Add", in, workflow.ActivityOptions{})
	if err != nil {
		return 0, err
	}
	return workflow.Activity[int, int](ctx, "Double", sum, workflow.ActivityOptions{})
}

func addActivity(_ context.Context, in pair) (int, error) {
	return in.A + in.B, nil
}

func doubleActivity(_ context.Context, n int) (int, error) {
	return n * 2, nil
}

func TestAddDoubleOnce(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 8, time.Second)
	stop := startAddDoubleWorkers(t, srv.URL, 1)
	defer stop()

	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "add_double", pair{A: 2, B: 3}, "")
	if err != nil {
		t.Fatal(err)
	}
	wf := waitWorkflow(t, c, id)
	if wf.Status != "completed" {
		t.Fatalf("status %s error %v", wf.Status, wf.Error)
	}
	var result int
	if err := json.Unmarshal(wf.Result, &result); err != nil || result != 10 {
		t.Fatalf("result %s", wf.Result)
	}

	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind   string
		seq    int
		name   string
		result int
		has    bool
	}{
		{kind: "WorkflowStarted"},
		{kind: "ActivityScheduled", seq: 1, name: "Add"},
		{kind: "ActivityCompleted", seq: 1, result: 5, has: true},
		{kind: "ActivityScheduled", seq: 2, name: "Double"},
		{kind: "ActivityCompleted", seq: 2, result: 10, has: true},
		{kind: "WorkflowCompleted", result: 10, has: true},
	}
	if len(events) != len(want) {
		t.Fatalf("history %d events, want %d: %+v", len(events), len(want), eventTypes(events))
	}
	for i, ev := range events {
		got := decodeEvent(t, ev)
		if ev.Type != want[i].kind || got.seq != want[i].seq || got.name != want[i].name {
			t.Fatalf("event %d = %s seq %d name %q, want %s seq %d name %q", i, ev.Type, got.seq, got.name, want[i].kind, want[i].seq, want[i].name)
		}
		if want[i].has && got.result != want[i].result {
			t.Fatalf("event %d result %d, want %d", i, got.result, want[i].result)
		}
	}
}

func TestAddDoubleMany(t *testing.T) {
	const n = 200
	srv := newQueueServer(t, 10*time.Second, 20, time.Second)
	stop := startAddDoubleWorkers(t, srv.URL, 4)
	defer stop()

	c := client.New(srv.URL, demoKey)
	for i := 0; i < n; i++ {
		if _, err := c.StartWorkflow(context.Background(), "add_double", pair{A: 2, B: 3}, ""); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var completed, failed int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FILTER (WHERE status = 'completed'),
			       count(*) FILTER (WHERE status = 'failed')
			FROM workflows
		`).Scan(&completed, &failed); err != nil {
			t.Fatal(err)
		}
		if failed > 0 {
			t.Fatalf("%d workflows failed\n%s", failed, workflowSample(t))
		}
		if completed == n {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	var completed, dupes int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM workflows WHERE status = 'completed' AND result::text = '10'
	`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != n {
		t.Fatalf("completed with result 10: %d\n%s", completed, workflowSample(t))
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM (
			SELECT workflow_id, data->>'command_seq' AS command_seq
			FROM workflow_events
			WHERE type = 'ActivityCompleted'
			GROUP BY workflow_id, data->>'command_seq'
			HAVING count(*) > 1
		) dupes
	`).Scan(&dupes); err != nil {
		t.Fatal(err)
	}
	if dupes != 0 {
		t.Fatalf("%d duplicate ActivityCompleted groups", dupes)
	}
}

func TestStaleWorkflowCompletion(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 4, 0)
	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "add_double", pair{A: 2, B: 3}, "stale-wf")
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, httpClient, srv.URL, "stale-worker")
	task := pollWorkflow(t, httpClient, srv.URL, workerID)

	var dispatched int
	if err := pool.QueryRow(context.Background(), `
		SELECT history_len_at_dispatch FROM tasks WHERE id = $1
	`, task.TaskID).Scan(&dispatched); err != nil {
		t.Fatal(err)
	}
	if dispatched != task.HistoryLen {
		t.Fatalf("history_len_at_dispatch %d, poll history_len %d", dispatched, task.HistoryLen)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE workflows SET history_len = history_len + 1 WHERE id = $1
	`, id); err != nil {
		t.Fatal(err)
	}

	res := postJSON(t, httpClient, fmt.Sprintf("%s/v1/tasks/%d/complete-workflow", srv.URL, task.TaskID), fmt.Sprintf(`{
		"lease_token": %d,
		"commands": [{"type":"ScheduleActivity","seq":1,"name":"ShouldNotApply","input":{"a":2,"b":3}}]
	}`, task.LeaseToken))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("complete status %d: %s", res.StatusCode, res.Body)
	}

	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if bytes.Contains(ev.Data, []byte("ShouldNotApply")) {
			t.Fatalf("stale command was written: %s", ev.Data)
		}
	}
	if len(events) != 1 || events[0].Type != "WorkflowStarted" {
		t.Fatalf("history = %+v", eventTypes(events))
	}

	var pending int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM tasks
		WHERE workflow_id = $1 AND kind = 'workflow' AND status = 'pending'
	`, id).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending workflow tasks = %d", pending)
	}
	var doneStatus string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1`, task.TaskID).Scan(&doneStatus); err != nil {
		t.Fatal(err)
	}
	if doneStatus != "done" {
		t.Fatalf("stale task status %s", doneStatus)
	}
}

func startAddDoubleWorkers(t *testing.T, base string, n int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		w := worker.New(worker.Options{
			Name:      fmt.Sprintf("add-double-%d", i),
			EngineURL: base,
			DemoKey:   demoKey,
		})
		w.RegisterWorkflow("add_double", addDouble)
		w.RegisterActivity("Add", addActivity)
		w.RegisterActivity("Double", doubleActivity)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := w.Run(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}()
	}
	return func() {
		cancel()
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Error(err)
		}
	}
}

func waitWorkflow(t *testing.T, c *client.Client, id string) client.Workflow {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last client.Workflow
	for time.Now().Before(deadline) {
		wf, err := c.GetWorkflow(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		last = wf
		if wf.Status == "completed" || wf.Status == "failed" {
			return wf
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("workflow %s stuck in %s\n%s", id, last.Status, workflowSample(t))
	return last
}

type decodedEvent struct {
	seq    int
	name   string
	result int
}

func decodeEvent(t *testing.T, ev client.Event) decodedEvent {
	t.Helper()
	var body struct {
		CommandSeq int    `json:"command_seq"`
		Name       string `json:"name"`
		Result     *int   `json:"result"`
	}
	if err := json.Unmarshal(ev.Data, &body); err != nil {
		t.Fatal(err)
	}
	got := decodedEvent{seq: body.CommandSeq, name: body.Name}
	if body.Result != nil {
		got.result = *body.Result
	}
	return got
}

func eventTypes(events []client.Event) []string {
	types := make([]string, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}

func workflowSample(t *testing.T) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id, status, coalesce(error, ''), history_len
		FROM workflows
		ORDER BY created_at
		LIMIT 5
	`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var buf bytes.Buffer
	for rows.Next() {
		var id, status, msg string
		var n int
		if err := rows.Scan(&id, &status, &msg, &n); err != nil {
			return err.Error()
		}
		fmt.Fprintf(&buf, "%s %s history %d %s\n", id, status, n, msg)
	}
	return buf.String()
}

type httpResult struct {
	StatusCode int
	Body       []byte
}

type workflowPoll struct {
	TaskID     int64 `json:"task_id"`
	LeaseToken int64 `json:"lease_token"`
	HistoryLen int   `json:"history_len"`
}

func pollWorkflow(t *testing.T, client *http.Client, base, workerID string) workflowPoll {
	t.Helper()
	res := postJSON(t, client, base+"/v1/tasks/poll", fmt.Sprintf(
		`{"worker_id":%q,"kinds":["workflow"],"wait_ms":2000}`, workerID,
	))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("poll status %d: %s", res.StatusCode, res.Body)
	}
	var task workflowPoll
	if err := json.Unmarshal(res.Body, &task); err != nil {
		t.Fatal(err)
	}
	if task.TaskID == 0 || task.LeaseToken == 0 {
		t.Fatalf("poll response %s", res.Body)
	}
	return task
}

func postJSON(t *testing.T, client *http.Client, url, body string) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResult{StatusCode: res.StatusCode, Body: payload}
}
