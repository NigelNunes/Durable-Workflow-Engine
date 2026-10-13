package workflow

import (
	"encoding/json"
	"testing"
	"time"
)

type pair struct {
	A int `json:"a"`
	B int `json:"b"`
}

type addIn struct {
	A  int       `json:"a"`
	B  int       `json:"b"`
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

type workflowResult struct {
	Value int       `json:"value"`
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
}

func addDouble(ctx Context, in pair) (workflowResult, error) {
	ctx.Logger().Info("before-add")
	sum, err := Activity[addIn, int](ctx, "Add", addIn{
		A: in.A, B: in.B, At: ctx.Now(), ID: ctx.UUID(),
	}, ActivityOptions{})
	if err != nil {
		return workflowResult{}, err
	}
	ctx.Logger().Info("after-add")
	doubled, err := Activity[int, int](ctx, "Double", sum, ActivityOptions{})
	if err != nil {
		return workflowResult{}, err
	}
	ctx.Logger().Info("after-double")
	return workflowResult{Value: doubled, At: ctx.Now(), ID: ctx.UUID()}, nil
}

func TestReplay(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	addDone := started.Add(time.Second)
	doubleDone := started.Add(2 * time.Second)

	tests := []struct {
		name    string
		history []HistoryEvent
		check   func(t *testing.T, commands []Command, failure *Failure)
	}{
		{
			name:    "empty history emits the first command",
			history: []HistoryEvent{workflowStarted(started)},
			check: func(t *testing.T, commands []Command, failure *Failure) {
				if failure != nil {
					t.Fatalf("failure %+v", failure)
				}
				if len(commands) != 1 {
					t.Fatalf("commands = %d, want 1", len(commands))
				}
				got := mustSchedule(t, commands[0], 1, "Add")
				var in addIn
				mustUnmarshal(t, got.Input, &in)
				if in.A != 2 || in.B != 3 || !in.At.Equal(started) || in.ID == "" {
					t.Fatalf("input %+v", in)
				}
			},
		},
		{
			name: "completed add emits double",
			history: []HistoryEvent{
				workflowStarted(started),
				activityScheduled(2, 1, "Add", started),
				activityCompleted(3, 1, 5, addDone),
				{Seq: 4, Type: "ActivityLeaseExpired", Data: json.RawMessage(`{"command_seq":1}`), CreatedAt: addDone},
			},
			check: func(t *testing.T, commands []Command, failure *Failure) {
				if failure != nil {
					t.Fatalf("failure %+v", failure)
				}
				if len(commands) != 1 {
					t.Fatalf("commands = %d, want 1", len(commands))
				}
				got := mustSchedule(t, commands[0], 2, "Double")
				var in int
				mustUnmarshal(t, got.Input, &in)
				if in != 5 {
					t.Fatalf("double input = %d", in)
				}
			},
		},
		{
			name: "full history completes the workflow",
			history: []HistoryEvent{
				workflowStarted(started),
				activityScheduled(2, 1, "Add", started),
				activityCompleted(3, 1, 5, addDone),
				activityScheduled(4, 2, "Double", addDone),
				activityCompleted(5, 2, 10, doubleDone),
			},
			check: func(t *testing.T, commands []Command, failure *Failure) {
				if failure != nil {
					t.Fatalf("failure %+v", failure)
				}
				if len(commands) != 1 {
					t.Fatalf("commands = %d, want 1", len(commands))
				}
				done, ok := commands[0].(CompleteWorkflow)
				if !ok {
					t.Fatalf("command = %T", commands[0])
				}
				var out workflowResult
				mustUnmarshal(t, done.Result, &out)
				if out.Value != 10 || !out.At.Equal(doubleDone) || out.ID == "" {
					t.Fatalf("result %+v", out)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, failure := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, tt.history)
			tt.check(t, commands, failure)
		})
	}
}

func TestReplayDeterminism(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	addDone := started.Add(time.Second)
	histories := [][]HistoryEvent{
		{workflowStarted(started)},
		{
			workflowStarted(started),
			activityScheduled(2, 1, "Add", started),
			activityCompleted(3, 1, 5, addDone),
		},
		{
			workflowStarted(started),
			activityScheduled(2, 1, "Add", started),
			activityCompleted(3, 1, 5, addDone),
			activityScheduled(4, 2, "Double", addDone),
			activityCompleted(5, 2, 10, started.Add(2*time.Second)),
		},
	}
	for i, history := range histories {
		first, fail1 := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, history)
		second, fail2 := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, history)
		if fail1 != nil || fail2 != nil {
			t.Fatalf("history %d failures %+v %+v", i, fail1, fail2)
		}
		if commandListJSON(t, first) != commandListJSON(t, second) {
			t.Fatalf("history %d commands differ\n%s\n%s", i, commandListJSON(t, first), commandListJSON(t, second))
		}
	}
}

func TestActivityNameMismatch(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	history := []HistoryEvent{
		workflowStarted(started),
		activityScheduled(2, 1, "Double", started),
	}
	commands, failure := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, history)
	if failure == nil || !failure.NonDeterminism {
		t.Fatalf("failure %+v", failure)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %#v", commands)
	}
	if failure.Message == "" {
		t.Fatal("empty message")
	}
}

func TestUnconsumedScheduledCommand(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	history := []HistoryEvent{
		workflowStarted(started),
		activityScheduled(2, 1, "Add", started),
		activityCompleted(3, 1, 5, started.Add(time.Second)),
		activityScheduled(4, 2, "Double", started.Add(time.Second)),
	}
	onlyAdd := func(ctx Context, in pair) (int, error) {
		return Activity[addIn, int](ctx, "Add", addIn{
			A: in.A, B: in.B, At: ctx.Now(), ID: ctx.UUID(),
		}, ActivityOptions{})
	}
	commands, failure := RunWorkflowTask(onlyAdd, "wf-1", pair{A: 2, B: 3}, history)
	if failure == nil || !failure.NonDeterminism {
		t.Fatalf("failure %+v", failure)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestUnrelatedPanicIsTaskFailure(t *testing.T) {
	boom := func(Context, struct{}) (struct{}, error) {
		panic("boom")
	}
	commands, failure := RunWorkflowTask(boom, "wf-1", struct{}{}, nil)
	if failure == nil || failure.NonDeterminism {
		t.Fatalf("failure %+v", failure)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want no FailWorkflow", commands)
	}
}

func TestSleep(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sleeper := func(ctx Context, _ struct{}) (string, error) {
		if err := ctx.Sleep(time.Minute); err != nil {
			return "", err
		}
		return "awake", nil
	}

	t.Run("new timer", func(t *testing.T) {
		commands, failure := RunWorkflowTask(sleeper, "wf-1", struct{}{}, []HistoryEvent{workflowStarted(started)})
		if failure != nil {
			t.Fatal(failure)
		}
		if len(commands) != 1 {
			t.Fatalf("commands = %d", len(commands))
		}
		timer, ok := commands[0].(StartTimer)
		if !ok || timer.Seq != 1 || !timer.FireAt.Equal(started.Add(time.Minute)) {
			t.Fatalf("command %+v", commands[0])
		}
	})

	t.Run("fired timer completes", func(t *testing.T) {
		fireAt := started.Add(time.Minute)
		history := []HistoryEvent{
			workflowStarted(started),
			timerStarted(2, 1, fireAt, started),
			timerFired(3, 1, fireAt),
		}
		commands, failure := RunWorkflowTask(sleeper, "wf-1", struct{}{}, history)
		if failure != nil {
			t.Fatal(failure)
		}
		if len(commands) != 1 {
			t.Fatalf("commands = %#v", commands)
		}
		done, ok := commands[0].(CompleteWorkflow)
		if !ok {
			t.Fatalf("command %T", commands[0])
		}
		var out string
		mustUnmarshal(t, done.Result, &out)
		if out != "awake" {
			t.Fatalf("result %s", out)
		}
	})

	t.Run("fire time mismatch", func(t *testing.T) {
		history := []HistoryEvent{
			workflowStarted(started),
			timerStarted(2, 1, started.Add(2*time.Minute), started),
		}
		commands, failure := RunWorkflowTask(sleeper, "wf-1", struct{}{}, history)
		if failure == nil || !failure.NonDeterminism {
			t.Fatalf("failure %+v", failure)
		}
		if len(commands) != 0 {
			t.Fatalf("commands = %#v", commands)
		}
	})
}

func TestActivityFailureFailsWorkflow(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	history := []HistoryEvent{
		workflowStarted(started),
		activityScheduled(2, 1, "Add", started),
		activityFailed(3, 1, "bad add", started.Add(time.Second)),
	}
	commands, failure := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, history)
	if failure != nil {
		t.Fatalf("task failure %+v", failure)
	}
	if len(commands) != 1 {
		t.Fatalf("commands = %#v", commands)
	}
	got, ok := commands[0].(FailWorkflow)
	if !ok || got.Error != "bad add" {
		t.Fatalf("command %+v", commands[0])
	}
}

func TestLoggerSkipsReplayedPrefix(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	history := []HistoryEvent{
		workflowStarted(started),
		activityScheduled(2, 1, "Add", started),
		activityCompleted(3, 1, 5, started.Add(time.Second)),
	}
	logs := &memLog{}
	commands, failure := RunWorkflowTask(addDouble, "wf-1", pair{A: 2, B: 3}, history, WithLogger(logs))
	if failure != nil {
		t.Fatal(failure)
	}
	if len(commands) != 1 {
		t.Fatalf("commands = %d", len(commands))
	}
	if _, ok := commands[0].(ScheduleActivity); !ok {
		t.Fatalf("command %T", commands[0])
	}
	if len(logs.msgs) != 1 || logs.msgs[0] != "after-add" {
		t.Fatalf("logs = %#v", logs.msgs)
	}
}

type memLog struct {
	msgs []string
}

func (m *memLog) Info(msg string, _ ...any) {
	m.msgs = append(m.msgs, msg)
}

func workflowStarted(at time.Time) HistoryEvent {
	return HistoryEvent{Seq: 1, Type: "WorkflowStarted", Data: json.RawMessage(`{"type":"add_double"}`), CreatedAt: at}
}

func activityScheduled(eventSeq, commandSeq int, name string, at time.Time) HistoryEvent {
	return HistoryEvent{Seq: eventSeq, Type: "ActivityScheduled", Data: mustRaw(map[string]any{
		"command_seq": commandSeq,
		"name":        name,
	}), CreatedAt: at}
}

func activityCompleted(eventSeq, commandSeq int, result any, at time.Time) HistoryEvent {
	return HistoryEvent{Seq: eventSeq, Type: "ActivityCompleted", Data: mustRaw(map[string]any{
		"command_seq": commandSeq,
		"result":      result,
	}), CreatedAt: at}
}

func activityFailed(eventSeq, commandSeq int, message string, at time.Time) HistoryEvent {
	return HistoryEvent{Seq: eventSeq, Type: "ActivityFailed", Data: mustRaw(map[string]any{
		"command_seq": commandSeq,
		"error":       message,
	}), CreatedAt: at}
}

func timerStarted(eventSeq, commandSeq int, fireAt, at time.Time) HistoryEvent {
	return HistoryEvent{Seq: eventSeq, Type: "TimerStarted", Data: mustRaw(struct {
		CommandSeq int       `json:"command_seq"`
		FireAt     time.Time `json:"fire_at"`
	}{commandSeq, fireAt}), CreatedAt: at}
}

func timerFired(eventSeq, commandSeq int, at time.Time) HistoryEvent {
	return HistoryEvent{Seq: eventSeq, Type: "TimerFired", Data: mustRaw(map[string]any{
		"command_seq": commandSeq,
	}), CreatedAt: at}
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func mustSchedule(t *testing.T, cmd Command, seq int, name string) ScheduleActivity {
	t.Helper()
	got, ok := cmd.(ScheduleActivity)
	if !ok || got.Seq != seq || got.Name != name {
		t.Fatalf("command = %#v, want ScheduleActivity seq %d %s", cmd, seq, name)
	}
	return got
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, dest any) {
	t.Helper()
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

func commandListJSON(t *testing.T, commands []Command) string {
	t.Helper()
	b, err := json.Marshal(commands)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
