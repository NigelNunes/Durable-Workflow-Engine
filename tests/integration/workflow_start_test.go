package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nigelnunes/durable-flow/internal/api"
	"github.com/nigelnunes/durable-flow/internal/history"
	"github.com/nigelnunes/durable-flow/internal/store"
	"github.com/nigelnunes/durable-flow/migrations"
)

const demoKey = "test-key"

var (
	dbURL string
	pool  *pgxpool.Pool
)

func TestMain(m *testing.M) {
	dbURL = os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	if _, err := store.Migrate(ctx, dbURL, migrations.FS); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	var err error
	pool, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestStartWorkflowsConcurrent(t *testing.T) {
	srv := newAPI(t)
	const n = 100

	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"id":"wf-%d","type":"order_fulfilment","input":{"n":%d}}`, i, i)
			ids[i], errs[i] = postWorkflow(srv.URL, demoKey, body)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("start wf-%d: %v", i, err)
		}
		if ids[i] != fmt.Sprintf("wf-%d", i) {
			t.Fatalf("id = %s, want wf-%d", ids[i], i)
		}
	}

	for i, id := range ids {
		events := getEvents(t, srv.URL, id)
		if len(events) != 1 {
			t.Fatalf("%s history len = %d, want 1", id, len(events))
		}
		if events[0].Seq != 1 || events[0].Type != history.WorkflowStarted {
			t.Fatalf("%s event = %+v, want seq 1 %s", id, events[0], history.WorkflowStarted)
		}
		wf := getWorkflow(t, srv.URL, id)
		if wf.Status != "running" || wf.HistoryLen != 1 {
			t.Fatalf("%s workflow = %+v", id, wf)
		}
		if wf.CurrentStep != "" || wf.OwnerWorker != "" || wf.Resumed {
			t.Fatalf("%s derived fields = step %q owner %q resumed %v", id, wf.CurrentStep, wf.OwnerWorker, wf.Resumed)
		}
		if i == 0 && !bytes.Contains(wf.Input, []byte(`"n": 0`)) && !bytes.Contains(wf.Input, []byte(`"n":0`)) {
			t.Fatalf("input = %s", wf.Input)
		}
	}

	assertCount(t, 0, `
		SELECT count(*) FROM (
			SELECT workflow_id, seq FROM workflow_events
			GROUP BY workflow_id, seq HAVING count(*) > 1
		) dups`)
	assertCount(t, 0, `
		SELECT count(*) FROM (
			SELECT workflow_id FROM workflow_events
			GROUP BY workflow_id
			HAVING min(seq) <> 1 OR max(seq) <> count(*) OR count(*) <> count(DISTINCT seq)
		) bad`)
	assertCount(t, n, `SELECT count(*) FROM workflows`)
	assertCount(t, n, `SELECT count(*) FROM tasks WHERE kind = 'workflow' AND status = 'pending'`)
}

func TestStartWorkflowDuplicateID(t *testing.T) {
	srv := newAPI(t)
	const id = "ord-dup"
	body := `{"id":"ord-dup","type":"order_fulfilment","input":{"amount_cents":9007199254740993}}`

	got, err := postWorkflow(srv.URL, demoKey, body)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("id = %s", got)
	}
	again, err := postWorkflow(srv.URL, demoKey, `{"id":"ord-dup","type":"other","input":{"nope":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("second id = %s", again)
	}

	assertCount(t, 1, `SELECT count(*) FROM workflows WHERE id = $1`, id)
	assertCount(t, 1, `SELECT count(*) FROM tasks WHERE workflow_id = $1`, id)
	assertCount(t, 1, `SELECT count(*) FROM workflow_events WHERE workflow_id = $1`, id)

	events := getEvents(t, srv.URL, id)
	if len(events) != 1 || events[0].Seq != 1 || events[0].Type != history.WorkflowStarted {
		t.Fatalf("events = %+v", events)
	}
	var data struct {
		Type  string          `json:"type"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Type != "order_fulfilment" {
		t.Fatalf("recorded type = %s", data.Type)
	}
	if !bytes.Contains(data.Input, []byte("9007199254740993")) {
		t.Fatalf("input lost precision: %s", data.Input)
	}

	wf := getWorkflow(t, srv.URL, id)
	if wf.Type != "order_fulfilment" {
		t.Fatalf("type changed to %s", wf.Type)
	}
	if !bytes.Contains(wf.Input, []byte("9007199254740993")) {
		t.Fatalf("workflow input = %s", wf.Input)
	}

	listed := listWorkflows(t, srv.URL, "/v1/workflows?status=running&type=order_fulfilment&limit=10")
	if len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("list = %+v", listed)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if older := listWorkflows(t, srv.URL, "/v1/workflows?before="+url.QueryEscape(past)); len(older) != 0 {
		t.Fatalf("before cursor returned %+v", older)
	}
}

func TestAuthAndHealth(t *testing.T) {
	srv := newAPI(t)
	body := `{"type":"order_fulfilment","input":null}`

	if status := postStatus(t, srv.URL, "", body); status != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d", status)
	}
	if status := postStatus(t, srv.URL, "wrong", body); status != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d", status)
	}
	assertCount(t, 0, `SELECT count(*) FROM workflows`)

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", res.StatusCode)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" {
		t.Fatalf("health = %+v", health)
	}

	id, err := postWorkflow(srv.URL, demoKey, body)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected a generated id")
	}
	assertCount(t, 1, `SELECT count(*) FROM workflows WHERE id = $1`, id)
	assertCount(t, 1, `SELECT count(*) FROM tasks WHERE workflow_id = $1`, id)
}

func newAPI(t *testing.T) *httptest.Server {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE workflows, workers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	srv := httptest.NewServer(api.NewHandler(st, demoKey))
	t.Cleanup(srv.Close)
	return srv
}

type historyEvent struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type workflowView struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input"`
	HistoryLen  int             `json:"history_len"`
	CurrentStep string          `json:"current_step"`
	OwnerWorker string          `json:"owner_worker"`
	Resumed     bool            `json:"resumed"`
}

func postWorkflow(base, key, body string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, base+"/v1/workflows", bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Demo-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", res.StatusCode, payload)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("empty id: %s", payload)
	}
	return out.ID, nil
}

func postStatus(t *testing.T, base, key, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/workflows", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-Demo-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func getEvents(t *testing.T, base, id string) []historyEvent {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/v1/workflows/"+id+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(res.Body)
		t.Fatalf("events status %d: %s", res.StatusCode, payload)
	}
	var events []historyEvent
	if err := json.NewDecoder(res.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	return events
}

func getWorkflow(t *testing.T, base, id string) workflowView {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/v1/workflows/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(res.Body)
		t.Fatalf("get status %d: %s", res.StatusCode, payload)
	}
	var wf workflowView
	if err := json.NewDecoder(res.Body).Decode(&wf); err != nil {
		t.Fatal(err)
	}
	return wf
}

func listWorkflows(t *testing.T, base, path string) []workflowView {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Demo-Key", demoKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(res.Body)
		t.Fatalf("list status %d: %s", res.StatusCode, payload)
	}
	var out struct {
		Workflows []workflowView `json:"workflows"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Workflows
}

func assertCount(t *testing.T, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d\n%s", got, want, query)
	}
}
