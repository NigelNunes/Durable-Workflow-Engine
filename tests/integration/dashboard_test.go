package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/internal/api"
	"github.com/nigelnunes/durable-flow/internal/history"
	"github.com/nigelnunes/durable-flow/internal/store"
)

const adminToken = "admin-token"

func TestChaosDeliveredOnce(t *testing.T) {
	srv, _ := openDemo(t, 60)
	client := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, client, srv.URL, "chaos-worker")

	status, body := postAdmin(t, client, srv.URL, "/v1/admin/chaos", `{"action":"crash"}`, adminToken)
	if status != http.StatusOK {
		t.Fatalf("chaos status %d: %s", status, body)
	}

	status, body = postJSONRaw(t, client, srv.URL, "/v1/workers/"+workerID+"/heartbeat", `{}`)
	if status != http.StatusOK {
		t.Fatalf("heartbeat status %d: %s", status, body)
	}
	var first struct {
		Chaos *string `json:"chaos"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if first.Chaos == nil || *first.Chaos != "crash" {
		t.Fatalf("chaos %+v", first.Chaos)
	}

	status, body = postJSONRaw(t, client, srv.URL, "/v1/workers/"+workerID+"/heartbeat", `{}`)
	if status != http.StatusOK {
		t.Fatalf("second heartbeat %d: %s", status, body)
	}
	var second struct {
		Chaos *string `json:"chaos"`
	}
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	if second.Chaos != nil {
		t.Fatalf("chaos delivered twice: %s", *second.Chaos)
	}
}

func TestStallAndChaosCooldown(t *testing.T) {
	srv, _ := openDemo(t, 60)
	client := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, client, srv.URL, "stall-worker")

	status, body := postAdmin(t, client, srv.URL, "/v1/admin/chaos", `{"action":"stall"}`, adminToken)
	if status != http.StatusOK {
		t.Fatalf("stall status %d: %s", status, body)
	}
	var pending *string
	if err := pool.QueryRow(context.Background(), `SELECT pending_chaos FROM workers WHERE id = $1`, workerID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending == nil || *pending != "stall:15" {
		t.Fatalf("pending_chaos %v", pending)
	}

	status, body = postAdmin(t, client, srv.URL, "/v1/admin/chaos", `{"action":"crash"}`, adminToken)
	if status != http.StatusTooManyRequests {
		t.Fatalf("cooldown status %d: %s", status, body)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "cooldown" {
		t.Fatalf("code %s", errBody.Code)
	}
}

func TestAdminAuth(t *testing.T) {
	off := newQueueServer(t, time.Second, 4, 0)
	client := &http.Client{Timeout: 5 * time.Second}
	status, body := postAdmin(t, client, off.URL, "/v1/admin/chaos", `{"action":"crash"}`, adminToken)
	if status != http.StatusNotFound {
		t.Fatalf("demo mode off status %d: %s", status, body)
	}
	status, _ = postAdmin(t, client, off.URL, "/v1/admin/restart-engine", `{}`, adminToken)
	if status != http.StatusNotFound {
		t.Fatalf("restart demo mode off status %d", status)
	}

	on, exits := openDemo(t, 60)
	status, body = postAdmin(t, client, on.URL, "/v1/admin/chaos", `{"action":"crash"}`, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("missing token status %d: %s", status, body)
	}

	status, body = postAdmin(t, client, on.URL, "/v1/admin/restart-engine", `{}`, adminToken)
	if status != http.StatusOK {
		t.Fatalf("restart status %d: %s", status, body)
	}
	select {
	case code := <-exits:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not exit")
	}
}

func TestRunningWorkflowCap(t *testing.T) {
	srv, _ := openDemo(t, 60)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflows (id, type, status, input)
		SELECT 'cap-' || g, 'noop', 'running', 'null'
		FROM generate_series(1, 60) AS g
	`); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	status, body := postJSONRaw(t, client, srv.URL, "/v1/workflows", `{"id":"cap-new","type":"noop","input":null}`)
	if status != http.StatusTooManyRequests {
		t.Fatalf("61st status %d: %s", status, body)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "too_many_workflows" {
		t.Fatalf("code %s", errBody.Code)
	}

	status, body = postJSONRaw(t, client, srv.URL, "/v1/workflows", `{"id":"cap-1","type":"noop","input":null}`)
	if status != http.StatusOK {
		t.Fatalf("existing id status %d: %s", status, body)
	}
}

func TestStartRateLimit(t *testing.T) {
	srv, _ := openDemo(t, 60)
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 1; i <= 12; i++ {
		status, body := postJSONRaw(t, client, srv.URL, "/v1/workflows", fmt.Sprintf(`{"id":"rate-%d","type":"noop","input":null}`, i))
		if status != http.StatusOK {
			t.Fatalf("start %d status %d: %s", i, status, body)
		}
	}
	status, body := postJSONRaw(t, client, srv.URL, "/v1/workflows", `{"id":"rate-13","type":"noop","input":null}`)
	if status != http.StatusTooManyRequests {
		t.Fatalf("13th status %d: %s", status, body)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "rate_limited" {
		t.Fatalf("code %s", errBody.Code)
	}
	time.Sleep(600 * time.Millisecond)
	status, body = postJSONRaw(t, client, srv.URL, "/v1/workflows", `{"id":"rate-14","type":"noop","input":null}`)
	if status != http.StatusOK {
		t.Fatalf("refilled start status %d: %s", status, body)
	}
}

func TestDashboardState(t *testing.T) {
	srv, _ := openDemo(t, 60)
	client := &http.Client{Timeout: 5 * time.Second}
	workerID := registerWorker(t, client, srv.URL, "dash-worker")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflows (id, type, status, input, result, history_len, created_at, updated_at)
		VALUES
			('dash-ok', 'order_fulfilment', 'completed', '{}', '{"status":"fulfilled"}', 5, now() - interval '10 seconds', now()),
			('dash-stale', 'noop', 'running', 'null', NULL, 2, now() - interval '9 seconds', now() - interval '1 second')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflow_events (workflow_id, seq, type, data, created_at) VALUES
			('dash-ok', 1, $1, '{}', now() - interval '10 seconds'),
			('dash-ok', 2, $2, '{"name":"ChargeCard"}', now() - interval '9 seconds'),
			('dash-ok', 3, $3, '{}', now() - interval '4 seconds'),
			('dash-ok', 4, $4, '{}', now() - interval '2 seconds'),
			('dash-ok', 5, $5, '{}', now() - interval '1 second'),
			('dash-stale', 1, $1, '{}', now() - interval '9 seconds'),
			('dash-stale', 2, $6, '{"command_seq":1}', now() - interval '3 seconds')
	`, history.WorkflowStarted, history.ActivityScheduled, history.ActivityLeaseExpired,
		history.ActivityCompleted, history.WorkflowCompleted, history.StaleCompletionRejected); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO tasks (workflow_id, kind, command_seq, activity_name, status, lease_owner, lease_token)
		VALUES ('dash-stale', 'activity', 1, 'Nop', 'leased', $1, 1)
	`, workerID); err != nil {
		t.Fatal(err)
	}

	status, body := getJSON(t, client, srv.URL, "/v1/dashboard/state")
	if status != http.StatusOK {
		t.Fatalf("dashboard status %d: %s", status, body)
	}
	var state struct {
		Workers []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Alive        bool   `json:"alive"`
			RunningTasks int    `json:"running_tasks"`
		} `json:"workers"`
		Workflows []struct {
			ID          string `json:"id"`
			CurrentStep string `json:"current_step"`
			StepIndex   int    `json:"step_index"`
			TotalSteps  int    `json:"total_steps"`
			OwnerWorker string `json:"owner_worker"`
			Resumed     bool   `json:"resumed"`
		} `json:"workflows"`
		Counters struct {
			Running                  int `json:"running"`
			Completed                int `json:"completed"`
			Failed                   int `json:"failed"`
			RecoveredAfterCrash      int `json:"recovered_after_crash"`
			StaleCompletionsRejected int `json:"stale_completions_rejected"`
			AvgRecoveryMS            int `json:"avg_recovery_ms"`
		} `json:"counters"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Workers) != 1 || state.Workers[0].Name != "dash-worker" || !state.Workers[0].Alive || state.Workers[0].RunningTasks != 1 {
		t.Fatalf("workers %+v", state.Workers)
	}
	var okWF, staleWF bool
	for _, wf := range state.Workflows {
		switch wf.ID {
		case "dash-ok":
			okWF = true
			if wf.CurrentStep != "ChargeCard" || wf.StepIndex != 0 || wf.TotalSteps != 3 || !wf.Resumed || wf.OwnerWorker != "" {
				t.Fatalf("dash-ok %+v", wf)
			}
		case "dash-stale":
			staleWF = true
			if wf.OwnerWorker != "dash-worker" || wf.Resumed || wf.TotalSteps != 0 {
				t.Fatalf("dash-stale %+v", wf)
			}
		}
	}
	if !okWF || !staleWF {
		t.Fatalf("workflows %+v", state.Workflows)
	}

	var want struct {
		Running, Completed, Failed, Recovered, Stale, Avg int
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM workflows WHERE status = 'running'),
			(SELECT count(*) FROM workflows WHERE status = 'completed'),
			(SELECT count(*) FROM workflows WHERE status = 'failed'),
			(SELECT count(*) FROM workflows w WHERE w.status = 'completed' AND EXISTS (
				SELECT 1 FROM workflow_events e WHERE e.workflow_id = w.id AND e.type = $1
			)),
			(SELECT count(*) FROM workflow_events WHERE type = $2),
			(SELECT COALESCE(round(avg(EXTRACT(EPOCH FROM (done.created_at - exp.created_at)) * 1000)), 0)::int
				FROM workflow_events exp
				JOIN LATERAL (
					SELECT created_at FROM workflow_events done
					WHERE done.workflow_id = exp.workflow_id AND done.seq > exp.seq AND done.type = $3
					ORDER BY done.seq LIMIT 1
				) done ON true
				WHERE exp.type = $1)
	`, history.ActivityLeaseExpired, history.StaleCompletionRejected, history.ActivityCompleted).Scan(
		&want.Running, &want.Completed, &want.Failed, &want.Recovered, &want.Stale, &want.Avg,
	); err != nil {
		t.Fatal(err)
	}
	got := state.Counters
	if got.Running != want.Running || got.Completed != want.Completed || got.Failed != want.Failed ||
		got.RecoveredAfterCrash != want.Recovered || got.StaleCompletionsRejected != want.Stale ||
		got.AvgRecoveryMS != want.Avg {
		t.Fatalf("counters %+v want %+v", got, want)
	}
	if got.RecoveredAfterCrash != 1 || got.StaleCompletionsRejected != 1 || got.AvgRecoveryMS != 2000 {
		t.Fatalf("fixture counters %+v", got)
	}

	status, body = getJSON(t, client, srv.URL, "/v1/workers")
	if status != http.StatusOK {
		t.Fatalf("workers status %d: %s", status, body)
	}
}

func openDemo(t *testing.T, maxRunning int) (*httptest.Server, chan int) {
	t.Helper()
	resetQueue(t)
	st, err := store.Open(context.Background(), dbURL, store.Config{MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	exits := make(chan int, 1)
	srv := httptest.NewServer(api.NewHandler(st, api.Config{
		DemoKey:             demoKey,
		LeaseTTL:            time.Second,
		HeartbeatInterval:   time.Second,
		DemoMode:            true,
		AdminToken:          adminToken,
		MaxRunningWorkflows: maxRunning,
		Exit: func(code int) {
			exits <- code
		},
	}))
	t.Cleanup(srv.Close)
	return srv, exits
}

func postAdmin(t *testing.T, client *http.Client, base, path, body, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, payload
}

func postJSONRaw(t *testing.T, client *http.Client, base, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewBufferString(body))
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
	payload, _ := io.ReadAll(res.Body)
	return res.StatusCode, payload
}

func getJSON(t *testing.T, client *http.Client, base, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
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
