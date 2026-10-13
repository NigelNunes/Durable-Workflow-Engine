package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/internal/engine"
	"github.com/nigelnunes/durable-flow/internal/store"
	"github.com/nigelnunes/durable-flow/sdk/client"
	"github.com/nigelnunes/durable-flow/sdk/worker"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

const sleepFor = 3 * time.Second

func TestSleepCompletesAfterThreeSeconds(t *testing.T) {
	srv, st := openEngine(t, 10*time.Second, 4, time.Second, store.DefaultMaxRedeliveries)
	startScheduler(t, st, 50*time.Millisecond)
	stop := startSleeper(t, srv.URL, sleepFor)
	defer stop()

	c := client.New(srv.URL, demoKey)
	started := time.Now()
	id, err := c.StartWorkflow(context.Background(), "sleeper", struct{}{}, "")
	if err != nil {
		t.Fatal(err)
	}
	wf := waitWorkflow(t, c, id)
	elapsed := time.Since(started)
	if wf.Status != "completed" {
		t.Fatalf("status %s error %v", wf.Status, wf.Error)
	}
	var result string
	if err := json.Unmarshal(wf.Result, &result); err != nil || result != "awake" {
		t.Fatalf("result %s", wf.Result)
	}
	if elapsed < sleepFor || elapsed > 4*time.Second {
		t.Fatalf("elapsed %s", elapsed)
	}
	events := historyOf(t, c, id)
	assertSleepHistory(t, events, sleepFor)
}

func TestSleepSurvivesSchedulerRestart(t *testing.T) {
	srv, st := openEngine(t, 10*time.Second, 4, time.Second, store.DefaultMaxRedeliveries)
	stopSched := startScheduler(t, st, 50*time.Millisecond)
	stop := startSleeper(t, srv.URL, sleepFor)
	defer stop()

	c := client.New(srv.URL, demoKey)
	started := time.Now()
	id, err := c.StartWorkflow(context.Background(), "sleeper", struct{}{}, "restart-sleep")
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, id, "TimerStarted")
	stopSched()
	time.Sleep(time.Second)
	startScheduler(t, st, 50*time.Millisecond)

	wf := waitWorkflow(t, c, id)
	elapsed := time.Since(started)
	if wf.Status != "completed" {
		t.Fatalf("status %s error %v", wf.Status, wf.Error)
	}
	if elapsed < sleepFor || elapsed > 4*time.Second {
		t.Fatalf("elapsed %s, timer clock was reset", elapsed)
	}
	assertSleepHistory(t, historyOf(t, c, id), sleepFor)
}

func TestSleepFiresPromptlyAfterDowntime(t *testing.T) {
	srv, st := openEngine(t, 10*time.Second, 4, time.Second, store.DefaultMaxRedeliveries)
	stopSched := startScheduler(t, st, 50*time.Millisecond)
	stop := startSleeper(t, srv.URL, sleepFor)
	defer stop()

	c := client.New(srv.URL, demoKey)
	id, err := c.StartWorkflow(context.Background(), "sleeper", struct{}{}, "late-sleep")
	if err != nil {
		t.Fatal(err)
	}
	started := waitEvent(t, c, id, "TimerStarted")
	var body struct {
		FireAt time.Time `json:"fire_at"`
	}
	if err := json.Unmarshal(started.Data, &body); err != nil {
		t.Fatal(err)
	}
	stopSched()
	wait := time.Until(body.FireAt) + 200*time.Millisecond
	if wait > 0 {
		time.Sleep(wait)
	}
	restarted := time.Now()
	startScheduler(t, st, 50*time.Millisecond)
	wf := waitWorkflow(t, c, id)
	if wf.Status != "completed" {
		t.Fatalf("status %s error %v", wf.Status, wf.Error)
	}
	if time.Since(restarted) > time.Second {
		t.Fatalf("late timer took %s after restart", time.Since(restarted))
	}
	assertSleepHistory(t, historyOf(t, c, id), sleepFor)
}

func TestTwoSchedulersFireEachTimerOnce(t *testing.T) {
	const n = 8
	const pause = 200 * time.Millisecond
	srv, st := openEngine(t, 10*time.Second, 8, time.Second, store.DefaultMaxRedeliveries)
	startScheduler(t, st, 20*time.Millisecond)
	startScheduler(t, st, 20*time.Millisecond)
	stop := startSleeper(t, srv.URL, pause)
	defer stop()

	c := client.New(srv.URL, demoKey)
	for i := 0; i < n; i++ {
		if _, err := c.StartWorkflow(context.Background(), "sleeper", struct{}{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var done int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM workflows WHERE status = 'completed' AND result::text = '"awake"'
		`).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done == n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var completed, fired, dupes int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM workflows WHERE status = 'completed'
	`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != n {
		t.Fatalf("completed %d", completed)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM workflow_events WHERE type = 'TimerFired'
	`).Scan(&fired); err != nil {
		t.Fatal(err)
	}
	if fired != n {
		t.Fatalf("TimerFired events %d", fired)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM (
			SELECT workflow_id
			FROM workflow_events
			WHERE type = 'TimerFired'
			GROUP BY workflow_id, data->>'command_seq'
			HAVING count(*) > 1
		) dupes
	`).Scan(&dupes); err != nil {
		t.Fatal(err)
	}
	if dupes != 0 {
		t.Fatalf("duplicate TimerFired groups %d", dupes)
	}
}

func startScheduler(t *testing.T, st *store.Store, every time.Duration) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go engine.RunScheduler(ctx, st, every)
	t.Cleanup(cancel)
	return cancel
}

func startSleeper(t *testing.T, base string, pause time.Duration) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := worker.New(worker.Options{Name: "sleeper", EngineURL: base, DemoKey: demoKey})
	w.RegisterWorkflow("sleeper", func(ctx workflow.Context, _ struct{}) (string, error) {
		if err := ctx.Sleep(pause); err != nil {
			return "", err
		}
		return "awake", nil
	})
	errCh := make(chan error, 1)
	go func() {
		err := w.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			t.Error(err)
		default:
		}
	})
	return cancel
}

func waitEvent(t *testing.T, c *client.Client, id, eventType string) client.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		events, err := c.GetHistory(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			if ev.Type == eventType {
				return ev
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("event %s not found", eventType)
	return client.Event{}
}

func historyOf(t *testing.T, c *client.Client, id string) []client.Event {
	t.Helper()
	events, err := c.GetHistory(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func assertSleepHistory(t *testing.T, events []client.Event, pause time.Duration) {
	t.Helper()
	want := []string{"WorkflowStarted", "TimerStarted", "TimerFired", "WorkflowCompleted"}
	if len(events) != len(want) {
		t.Fatalf("history %v", eventTypes(events))
	}
	for i, ev := range events {
		if ev.Type != want[i] {
			t.Fatalf("event %d %s, want %s", i, ev.Type, want[i])
		}
	}
	var timer struct {
		FireAt time.Time `json:"fire_at"`
	}
	if err := json.Unmarshal(events[1].Data, &timer); err != nil {
		t.Fatal(err)
	}
	delta := timer.FireAt.Sub(events[0].CreatedAt)
	if delta < pause-time.Millisecond || delta > pause+time.Millisecond {
		t.Fatalf("fire_at delta %s, want %s", delta, pause)
	}
}
