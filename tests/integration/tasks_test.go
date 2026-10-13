package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/internal/api"
	"github.com/nigelnunes/durable-flow/internal/engine"
	"github.com/nigelnunes/durable-flow/internal/history"
	"github.com/nigelnunes/durable-flow/internal/store"
)

func TestPollLeasesEachTaskOnce(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 50, 0)
	client := &http.Client{Timeout: 10 * time.Second}
	for round := 0; round < 20; round++ {
		resetQueue(t)
		insertActivityTasks(t, 1000)
		workers := make([]string, 50)
		for i := range workers {
			workers[i] = registerWorker(t, client, srv.URL, fmt.Sprintf("poller-%d", i))
		}

		var (
			mu  sync.Mutex
			ids []int64
			wg  sync.WaitGroup
		)
		errCh := make(chan error, len(workers))
		for _, workerID := range workers {
			wg.Add(1)
			go func(workerID string) {
				defer wg.Done()
				for {
					status, task, err := pollActivity(client, srv.URL, workerID, 0)
					if err != nil {
						errCh <- err
						return
					}
					if status == http.StatusNoContent {
						return
					}
					if status != http.StatusOK {
						errCh <- fmt.Errorf("poll status %d", status)
						return
					}
					if task.Kind != "activity" || task.IdempotencyKey != fmt.Sprintf("queue-wf:%d", task.CommandSeq) {
						errCh <- fmt.Errorf("task %+v", task)
						return
					}
					mu.Lock()
					ids = append(ids, task.TaskID)
					mu.Unlock()
				}
			}(workerID)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatalf("round %d: %v", round, err)
		}

		seen := make(map[int64]int, len(ids))
		for _, id := range ids {
			seen[id]++
		}
		if len(seen) != 1000 {
			t.Fatalf("round %d: leased %d distinct tasks", round, len(seen))
		}
		for id, n := range seen {
			if n != 1 {
				t.Fatalf("round %d: task %d leased %d times", round, id, n)
			}
		}
		assertCount(t, 1000, `SELECT count(*) FROM tasks WHERE status = 'leased' AND lease_token = 1`)
		assertCount(t, 0, `SELECT count(*) FROM tasks WHERE status = 'pending'`)
	}
}

func TestLeaseExpiryRequeuesTask(t *testing.T) {
	srv := newQueueServer(t, time.Second, 4, 200*time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, client, srv.URL, "reaper-worker")
	insertActivityTasks(t, 1)

	status, first, err := pollActivity(client, srv.URL, workerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("poll status %d", status)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var (
			taskStatus   string
			redeliveries int
			owner        *string
			token        int64
		)
		err := pool.QueryRow(context.Background(), `
			SELECT status, redeliveries, lease_owner, lease_token FROM tasks WHERE id = $1
		`, first.TaskID).Scan(&taskStatus, &redeliveries, &owner, &token)
		if err != nil {
			t.Fatal(err)
		}
		if taskStatus == "pending" && redeliveries == 1 && owner == nil {
			if token != first.LeaseToken {
				t.Fatalf("reaper changed token from %d to %d", first.LeaseToken, token)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task not requeued: status=%s redeliveries=%d owner=%v", taskStatus, redeliveries, owner)
		}
		time.Sleep(50 * time.Millisecond)
	}

	status, again, err := pollActivity(client, srv.URL, workerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("second poll status %d", status)
	}
	if again.TaskID != first.TaskID {
		t.Fatalf("second poll task %d, want %d", again.TaskID, first.TaskID)
	}
	if again.LeaseToken <= first.LeaseToken {
		t.Fatalf("lease token %d, want greater than %d", again.LeaseToken, first.LeaseToken)
	}
}

func TestStaleTaskHeartbeat(t *testing.T) {
	srv := newQueueServer(t, time.Second, 4, 0)
	client := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, client, srv.URL, "heartbeat-worker")
	insertActivityTasks(t, 1)

	status, task, err := pollActivity(client, srv.URL, workerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("poll status %d", status)
	}

	rewindLease(t, task.TaskID)
	before := leaseExpiry(t, task.TaskID)
	if code := postTaskHeartbeat(client, srv.URL, task.TaskID, task.LeaseToken-1); code != http.StatusConflict {
		t.Fatalf("stale heartbeat status %d", code)
	}
	if got := leaseExpiry(t, task.TaskID); !got.Equal(before) {
		t.Fatalf("stale heartbeat moved expiry from %s to %s", before, got)
	}

	if code := postTaskHeartbeat(client, srv.URL, task.TaskID, task.LeaseToken); code != http.StatusOK {
		t.Fatalf("heartbeat status %d", code)
	}
	if got := leaseExpiry(t, task.TaskID); !got.After(time.Now()) {
		t.Fatalf("expiry %s is not in the future", got)
	}

	rewindLease(t, task.TaskID)
	if code := postWorkerHeartbeat(client, srv.URL, workerID); code != http.StatusOK {
		t.Fatalf("worker heartbeat status %d", code)
	}
	if got := leaseExpiry(t, task.TaskID); !got.After(time.Now()) {
		t.Fatalf("worker heartbeat left expiry at %s", got)
	}
}

func TestPollWorkflowTaskReturnsHistory(t *testing.T) {
	srv := newQueueServer(t, 10*time.Second, 4, 0)
	client := &http.Client{Timeout: 5 * time.Second}
	workflowID, err := postWorkflow(srv.URL, demoKey, `{"id":"wf-poll","type":"order_fulfilment","input":{"n":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	workerID := registerWorker(t, client, srv.URL, "workflow-worker")

	status, payload := pollRaw(t, client, srv.URL, workerID, []string{"workflow"}, 0)
	if status != http.StatusOK {
		t.Fatalf("poll status %d: %s", status, payload)
	}
	var resp struct {
		Kind       string `json:"kind"`
		LeaseToken int64  `json:"lease_token"`
		HistoryLen int    `json:"history_len"`
		Workflow   struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"workflow"`
		History []struct {
			Seq  int    `json:"seq"`
			Type string `json:"type"`
		} `json:"history"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "workflow" || resp.LeaseToken != 1 || resp.HistoryLen != 1 {
		t.Fatalf("poll response %+v", resp)
	}
	if resp.Workflow.ID != workflowID || resp.Workflow.Type != "order_fulfilment" {
		t.Fatalf("workflow %+v", resp.Workflow)
	}
	if len(resp.History) != 1 || resp.History[0].Seq != 1 || resp.History[0].Type != history.WorkflowStarted {
		t.Fatalf("history %+v", resp.History)
	}
}

func newQueueServer(t *testing.T, leaseTTL time.Duration, maxConns int32, reaperEvery time.Duration) *httptest.Server {
	t.Helper()
	return startQueueServer(t, leaseTTL, maxConns, reaperEvery, store.DefaultMaxRedeliveries)
}

func startQueueServer(t *testing.T, leaseTTL time.Duration, maxConns int32, reaperEvery time.Duration, maxRedeliveries int) *httptest.Server {
	t.Helper()
	resetQueue(t)
	st, err := store.Open(context.Background(), dbURL, store.Config{MaxConns: maxConns})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if reaperEvery > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go engine.RunReaper(ctx, st, reaperEvery, maxRedeliveries)
	}
	srv := httptest.NewServer(api.NewHandler(st, api.Config{
		DemoKey:           demoKey,
		LeaseTTL:          leaseTTL,
		HeartbeatInterval: time.Second,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func resetQueue(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE workflows, workers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func insertActivityTasks(t *testing.T, n int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflows (id, type, status, input)
		VALUES ('queue-wf', 'noop', 'running', 'null')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO tasks (workflow_id, kind, command_seq, activity_name, status)
		SELECT 'queue-wf', 'activity', g, 'Nop', 'pending'
		FROM generate_series(1, $1) AS g
	`, n); err != nil {
		t.Fatal(err)
	}
}

type activityPoll struct {
	TaskID         int64  `json:"task_id"`
	Kind           string `json:"kind"`
	LeaseToken     int64  `json:"lease_token"`
	CommandSeq     int    `json:"command_seq"`
	IdempotencyKey string `json:"idempotency_key"`
}

func registerWorker(t *testing.T, client *http.Client, base, name string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/workers/register", bytes.NewBufferString(fmt.Sprintf(`{"name":%q}`, name)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register status %d: %s", res.StatusCode, payload)
	}
	var out struct {
		WorkerID            string `json:"worker_id"`
		LeaseTTLMS          int64  `json:"lease_ttl_ms"`
		HeartbeatIntervalMS int64  `json:"heartbeat_interval_ms"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.WorkerID == "" || out.LeaseTTLMS <= 0 || out.HeartbeatIntervalMS <= 0 {
		t.Fatalf("register response %+v", out)
	}
	return out.WorkerID
}

func pollActivity(client *http.Client, base, workerID string, waitMS int) (int, activityPoll, error) {
	req, err := http.NewRequest(http.MethodPost, base+"/v1/tasks/poll", bytes.NewBufferString(
		fmt.Sprintf(`{"worker_id":%q,"kinds":["activity"],"wait_ms":%d}`, workerID, waitMS),
	))
	if err != nil {
		return 0, activityPoll{}, err
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := client.Do(req)
	if err != nil {
		return 0, activityPoll{}, err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, activityPoll{}, nil
	}
	var task activityPoll
	if err := json.Unmarshal(payload, &task); err != nil {
		return res.StatusCode, activityPoll{}, err
	}
	return res.StatusCode, task, nil
}

func pollRaw(t *testing.T, client *http.Client, base, workerID string, kinds []string, waitMS int) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"worker_id": workerID,
		"kinds":     kinds,
		"wait_ms":   waitMS,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/tasks/poll", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, payload
}

func postTaskHeartbeat(client *http.Client, base string, taskID, token int64) int {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/tasks/%d/heartbeat", base, taskID),
		bytes.NewBufferString(fmt.Sprintf(`{"lease_token":%d}`, token)))
	if err != nil {
		return 0
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func postWorkerHeartbeat(client *http.Client, base, workerID string) int {
	req, err := http.NewRequest(http.MethodPost, base+"/v1/workers/"+workerID+"/heartbeat", bytes.NewReader(nil))
	if err != nil {
		return 0
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func rewindLease(t *testing.T, taskID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE tasks SET lease_expires_at = now() - interval '30 seconds' WHERE id = $1
	`, taskID); err != nil {
		t.Fatal(err)
	}
}

func leaseExpiry(t *testing.T, taskID int64) time.Time {
	t.Helper()
	var expiry time.Time
	if err := pool.QueryRow(context.Background(), `SELECT lease_expires_at FROM tasks WHERE id = $1`, taskID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	return expiry
}
