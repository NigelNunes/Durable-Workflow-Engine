package chaos

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/client"
)

func TestF1KillBeforeCharge(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f1", "victim", "1", "victim")
		c.waitWorkers(1)
		id := c.start("charge_once", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "before-charge" {
			t.Fatalf("phase %s", phase)
		}
		c.workers[0].kill()
		charges, blocked := c.fake.snapshot()
		if charges != 0 || blocked != 0 {
			t.Fatalf("charges %d blocked %d before redelivery", charges, blocked)
		}
		c.workers = nil
		c.startWorker("f1", "other", "1", "other")
		wf := c.waitStatus(id, "completed", 8*time.Second)
		if !strings.HasPrefix(resultString(t, wf.Result), "ch-") {
			t.Fatalf("result %s", wf.Result)
		}
		events := c.events(id)
		if countType(events, "ActivityCompleted") != 1 || countType(events, "WorkflowCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		charges, blocked = c.fake.snapshot()
		if charges != 1 || blocked != 0 {
			t.Fatalf("charges %d blocked %d", charges, blocked)
		}
		c.waitNoLeased()
	})
}

func TestF2KillAfterCharge(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f2", "victim", "1", "victim")
		c.waitWorkers(1)
		id := c.start("charge_once", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "after-charge" {
			t.Fatalf("phase %s", phase)
		}
		c.workers[0].kill()
		c.workers = nil
		c.startWorker("f2", "other", "1", "other")
		c.waitStatus(id, "completed", 8*time.Second)
		events := c.events(id)
		if countType(events, "ActivityCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		charges, blocked := c.fake.snapshot()
		if charges != 1 || blocked != 1 {
			t.Fatalf("charges %d blocked %d", charges, blocked)
		}
		c.waitNoLeased()
	})
}

func TestF3KillDuringWorkflowTask(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f3", "victim", "1", "victim")
		c.waitWorkers(1)
		id := c.start("replay_kill", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "workflow" {
			t.Fatalf("phase %s", phase)
		}
		c.workers[0].kill()
		c.workers = nil
		events := c.events(id)
		if len(events) != 1 || events[0].Type != "WorkflowStarted" {
			t.Fatalf("history after kill %v", eventTypes(events))
		}
		c.startWorker("f3", "other", "1", "other")
		wf := c.waitStatus(id, "completed", 8*time.Second)
		if resultString(t, wf.Result) != "ok" {
			t.Fatalf("result %s", wf.Result)
		}
		events = c.events(id)
		for _, typ := range []string{"WorkflowStarted", "ActivityScheduled", "ActivityCompleted", "WorkflowCompleted"} {
			if countType(events, typ) != 1 {
				t.Fatalf("%s count != 1 in %v", typ, eventTypes(events))
			}
		}
		c.waitNoLeased()
	})
}

func TestF4StallThenStaleComplete(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f4", "victim", "1", "victim")
		c.waitWorkers(1)
		id := c.start("stall", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "stalled" {
			t.Fatalf("phase %s", phase)
		}
		c.startWorker("f4", "other", "1", "other")
		wf := c.waitStatus(id, "completed", 8*time.Second)
		if resultString(t, wf.Result) != "other" {
			t.Fatalf("result %s", wf.Result)
		}
		c.waitEventCount(id, "StaleCompletionRejected", 1, 6*time.Second)
		events := c.events(id)
		if countType(events, "ActivityCompleted") != 1 || countType(events, "StaleCompletionRejected") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		c.waitNoLeased()
	})
}

func TestF5ActivityRetries(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f5", "other", "1", "flaky")
		c.waitWorkers(1)
		c.fake.setFailing(true)
		id := c.start("flaky", map[string]any{"max_attempts": 4})
		c.waitEventCount(id, "ActivityRetryScheduled", 2, 5*time.Second)
		c.fake.setFailing(false)
		wf := c.waitStatus(id, "completed", 5*time.Second)
		if resultString(t, wf.Result) != "ok" {
			t.Fatalf("result %s", wf.Result)
		}
		events := c.events(id)
		if countType(events, "ActivityRetryScheduled") < 2 || countType(events, "ActivityCompleted") != 1 || countType(events, "ActivityFailed") != 0 {
			t.Fatalf("history %v", eventTypes(events))
		}

		c.fake.setFailing(true)
		failedID := c.start("flaky", map[string]any{"max_attempts": 2})
		failed := c.waitStatus(failedID, "failed", 5*time.Second)
		if failed.Error == nil || *failed.Error == "" {
			t.Fatal("missing workflow error")
		}
		failedEvents := c.events(failedID)
		if countType(failedEvents, "ActivityFailed") != 1 || countType(failedEvents, "ActivityCompleted") != 0 {
			t.Fatalf("history %v", eventTypes(failedEvents))
		}
		c.waitNoLeased()
	})
}

func TestF6EngineRestart(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f6", "other", "1", "holder")
		c.waitWorkers(1)
		id := c.start("hold", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "holding" {
			t.Fatalf("phase %s", phase)
		}
		c.stopEngine()
		time.Sleep(time.Second)
		c.launchEngine()
		c.waitStatus(id, "completed", 12*time.Second)
		events := c.events(id)
		if countType(events, "WorkflowStarted") != 1 || countType(events, "ActivityCompleted") != 1 || countType(events, "WorkflowCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		c.waitNoLeased()
	})
}

func TestF7PostgresRestart(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f7", "other", "1", "holder")
		c.waitWorkers(1)
		id := c.start("hold", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "holding" {
			t.Fatalf("phase %s", phase)
		}
		restartPostgres(t)
		c.waitStatus(id, "completed", 60*time.Second)
		events := c.events(id)
		if countType(events, "WorkflowStarted") != 1 || countType(events, "WorkflowCompleted") != 1 || countType(events, "ActivityCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		if _, err := c.http.GetWorkflow(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		c.waitNoLeased()
	})
}

func TestF8EngineDownPastFireAt(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f8", "other", "1", "sleeper")
		c.waitWorkers(1)
		id := c.start("sleeper", struct{}{})
		c.waitEventCount(id, "TimerStarted", 1, 5*time.Second)
		var timer struct {
			FireAt time.Time `json:"fire_at"`
		}
		var found bool
		for _, ev := range c.events(id) {
			if ev.Type != "TimerStarted" {
				continue
			}
			if err := json.Unmarshal(ev.Data, &timer); err != nil {
				t.Fatal(err)
			}
			found = true
		}
		if !found {
			t.Fatal("TimerStarted missing")
		}
		if time.Until(timer.FireAt) < 200*time.Millisecond {
			t.Fatalf("fire_at %s is already due", timer.FireAt)
		}
		c.stopEngine()
		if wait := time.Until(timer.FireAt) + 200*time.Millisecond; wait > 0 {
			time.Sleep(wait)
		}
		c.launchEngine()
		restarted := time.Now()
		c.waitStatus(id, "completed", 2*time.Second)
		if time.Since(restarted) > 2*time.Second {
			t.Fatalf("completed %s after engine start", time.Since(restarted))
		}
		events := c.events(id)
		if countType(events, "TimerFired") != 1 || countType(events, "WorkflowCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		c.waitNoLeased()
	})
}

func TestF9DuplicateCompletion(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f9", "victim", "1", "victim")
		c.waitWorkers(1)
		id := c.start("hold", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "holding" {
			t.Fatalf("phase %s", phase)
		}
		c.rewindActivityLeases()
		time.Sleep(150 * time.Millisecond)
		c.rewindActivityLeases()
		c.startWorker("f9", "other", "1", "other")
		wf := c.waitStatus(id, "completed", 8*time.Second)
		if resultString(t, wf.Result) != "from-b" {
			t.Fatalf("result %s", wf.Result)
		}
		c.waitEventCount(id, "StaleCompletionRejected", 1, 5*time.Second)
		events := c.events(id)
		if countType(events, "ActivityCompleted") != 1 {
			t.Fatalf("history %v", eventTypes(events))
		}
		c.waitNoLeased()
	})
}

func TestF10NonDeterminism(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		c.startWorker("f10", "victim", "1", "v1")
		c.waitWorkers(1)
		id := c.start("versioned", struct{}{})
		if phase := c.fake.waitReady(t, 5*time.Second); phase != "step-a" {
			t.Fatalf("phase %s", phase)
		}
		c.workers[0].kill()
		c.workers = nil
		c.startWorker("f10", "other", "2", "v2")
		wf := c.waitStatus(id, "failed", 8*time.Second)
		if wf.Error == nil || !strings.Contains(*wf.Error, "non-determinism") {
			t.Fatalf("error %v", wf.Error)
		}
		events := c.events(id)
		var failed *client.Event
		for i := range events {
			if events[i].Type == "WorkflowFailed" {
				failed = &events[i]
			}
		}
		if failed == nil {
			t.Fatalf("history %v", eventTypes(events))
		}
		var body struct {
			NonDeterminism bool `json:"non_determinism"`
		}
		if err := json.Unmarshal(failed.Data, &body); err != nil {
			t.Fatal(err)
		}
		if !body.NonDeterminism {
			t.Fatalf("WorkflowFailed data %s", failed.Data)
		}
		if eventNames(events, "StepB") != 0 {
			t.Fatalf("StepB ran: %v", eventTypes(events))
		}
		c.waitNoLeased()
	})
}

func TestF11PoisonActivity(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 2)
		id := ""
		for i := 0; i < 3; i++ {
			p := c.startWorker("f11", "other", "1", "")
			if i == 0 {
				c.waitWorkers(1)
				id = c.start("poison", struct{}{})
			}
			c.waitExit(p, 8*time.Second)
		}
		c.waitEventCount(id, "ActivityFailed", 1, 8*time.Second)
		events := c.events(id)
		var activityFailed bool
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
			if body.Error != "TooManyRedeliveries" {
				t.Fatalf("activity error %s", body.Error)
			}
			activityFailed = true
		}
		if !activityFailed {
			t.Fatalf("history %v", eventTypes(events))
		}
		if n := c.openActivityTasks(); n != 0 {
			t.Fatalf("open activity tasks %d", n)
		}
		c.startWorker("plain", "other", "1", "healthy")
		failed := c.waitStatus(id, "failed", 8*time.Second)
		if failed.Error == nil || !strings.Contains(*failed.Error, "TooManyRedeliveries") {
			t.Fatalf("workflow error %v", failed.Error)
		}
		echo := c.start("plain", struct{}{})
		wf := c.waitStatus(echo, "completed", 8*time.Second)
		if resultString(t, wf.Result) != "ok" {
			t.Fatalf("result %s", wf.Result)
		}
		if !c.waitHealth(2 * time.Second) {
			t.Fatal("engine is not healthy")
		}
		c.waitNoLeased()
	})
}

func TestF12LoadAndKill(t *testing.T) {
	repeat(t, func(t *testing.T) {
		c := newCluster(t, 20)
		for i := 0; i < 3; i++ {
			c.startWorker("f12", "other", "1", "")
		}
		c.waitWorkers(3)
		started := time.Now()
		for i := 0; i < 200; i++ {
			c.start("charge_once", struct{}{})
		}
		nextKill := time.Now().Add(10 * time.Second)
		deadline := started.Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if c.countStatus("completed") == 200 {
				break
			}
			if failed := c.countStatus("failed"); failed != 0 {
				t.Fatalf("%d workflows failed", failed)
			}
			if time.Now().After(nextKill) {
				c.killRandomAndReplace("f12")
				nextKill = time.Now().Add(10 * time.Second)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if got := c.countStatus("completed"); got != 200 {
			t.Fatalf("completed %d", got)
		}
		if time.Since(started) > 60*time.Second {
			t.Fatalf("elapsed %s", time.Since(started))
		}
		charges, _ := c.fake.snapshot()
		if charges != 200 {
			t.Fatalf("charges %d", charges)
		}
		var dupes int
		if err := pool.QueryRow(t.Context(), `
			SELECT count(*) FROM (
				SELECT workflow_id
				FROM workflow_events
				WHERE type = 'ActivityCompleted'
				GROUP BY workflow_id
				HAVING count(*) > 1
			) dupes
		`).Scan(&dupes); err != nil {
			t.Fatal(err)
		}
		if dupes != 0 {
			t.Fatalf("duplicate ActivityCompleted groups %d", dupes)
		}
		c.waitNoLeased()
	})
}
