package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	eventWorkflowStarted   = "WorkflowStarted"
	eventActivityScheduled = "ActivityScheduled"
	eventActivityCompleted = "ActivityCompleted"
	eventActivityFailed    = "ActivityFailed"
	eventTimerStarted      = "TimerStarted"
	eventTimerFired        = "TimerFired"
)

var errBlocked = errors.New("workflow blocked")

type nonDeterminismError struct {
	Seq      int
	Expected string
	Got      string
}

func (e *nonDeterminismError) Error() string {
	if e == nil {
		return "non-determinism"
	}
	return fmt.Sprintf("non-determinism at command %d: history has %s, code has %s", e.Seq, e.Expected, e.Got)
}

type scheduledCmd struct {
	Kind   string
	Name   string
	FireAt time.Time
}

type commandDone struct {
	Failed       bool
	Error        string
	NonRetryable bool
	Result       json.RawMessage
	TimerFired   bool
}

type wfContext struct {
	workflowID       string
	now              time.Time
	seq              int
	uuidN            int
	replaying        bool
	lastScheduledSeq int
	logger           Logger
	commands         []Command
	scheduled        map[int]scheduledCmd
	done             map[int]commandDone
	commandTime      map[int]time.Time
	visited          map[int]bool
}

func (c *wfContext) WorkflowID() string { return c.workflowID }

func (c *wfContext) Now() time.Time { return c.now }

func (c *wfContext) UUID() string {
	c.uuidN++
	return uuidV5(workflowUUIDNamespace, fmt.Sprintf("%s:%d", c.workflowID, c.uuidN))
}

func (c *wfContext) Logger() Logger { return c }

func (c *wfContext) Info(msg string, args ...any) {
	if c.replaying || c.logger == nil {
		return
	}
	c.logger.Info(msg, args...)
}

func (c *wfContext) Sleep(d time.Duration) error {
	seq := c.nextSeq()
	expected := c.now.Add(d)
	sched, ok := c.scheduled[seq]
	if !ok {
		c.commands = append(c.commands, StartTimer{Seq: seq, FireAt: expected})
		panic(errBlocked)
	}
	if sched.Kind != "timer" || !sched.FireAt.Equal(expected) {
		got := expected.Format(time.RFC3339Nano)
		want := sched.Name
		if sched.Kind == "timer" {
			want = sched.FireAt.Format(time.RFC3339Nano)
		}
		panic(&nonDeterminismError{Seq: seq, Expected: want, Got: got})
	}
	finished, ok := c.done[seq]
	if !ok || !finished.TimerFired {
		panic(errBlocked)
	}
	c.acceptClock(seq)
	c.finishReplay(seq)
	return nil
}

func (c *wfContext) nextSeq() int {
	c.seq++
	c.visited[c.seq] = true
	return c.seq
}

func (c *wfContext) acceptClock(seq int) {
	at, ok := c.commandTime[seq]
	if ok && at.After(c.now) {
		c.now = at
	}
}

func (c *wfContext) finishReplay(seq int) {
	if seq >= c.lastScheduledSeq {
		c.replaying = false
	}
}

func (c *wfContext) unconsumedSeq() (int, bool) {
	var seqs []int
	for seq := range c.scheduled {
		if !c.visited[seq] {
			seqs = append(seqs, seq)
		}
	}
	if len(seqs) == 0 {
		return 0, false
	}
	sort.Ints(seqs)
	return seqs[0], true
}

// Activity replays a recorded activity or emits ScheduleActivity and stops.
func Activity[In, Out any](ctx Context, name string, in In, opts ActivityOptions) (Out, error) {
	var zero Out
	c, ok := ctx.(*wfContext)
	if !ok {
		panic("workflow.Activity requires the replay context")
	}
	seq := c.nextSeq()
	input, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Errorf("encode activity input: %w", err))
	}
	sched, found := c.scheduled[seq]
	if !found {
		c.commands = append(c.commands, ScheduleActivity{
			Seq:   seq,
			Name:  name,
			Input: input,
			Retry: opts.Retry,
		})
		panic(errBlocked)
	}
	if sched.Kind != "activity" || sched.Name != name {
		expected := sched.Name
		if sched.Kind == "timer" {
			expected = "Sleep"
		}
		panic(&nonDeterminismError{Seq: seq, Expected: expected, Got: name})
	}
	finished, ok := c.done[seq]
	if !ok {
		panic(errBlocked)
	}
	c.acceptClock(seq)
	c.finishReplay(seq)
	if finished.Failed {
		return zero, &ApplicationError{Message: finished.Error, NonRetryable: finished.NonRetryable}
	}
	var out Out
	if len(finished.Result) > 0 && string(finished.Result) != "null" {
		if err := json.Unmarshal(finished.Result, &out); err != nil {
			panic(fmt.Errorf("decode activity result: %w", err))
		}
	}
	return out, nil
}

// RunWorkflowTask replays fn against history.
// commands are the new commands from this run. failure is set when the task
// itself cannot be applied. A nil failure with commands means the run is usable.
func RunWorkflowTask[In, Out any](fn func(Context, In) (Out, error), workflowID string, input In, history []HistoryEvent, opts ...Option) (commands []Command, failure *Failure) {
	c, err := newContext(workflowID, history)
	if err != nil {
		return nil, &Failure{Message: err.Error()}
	}
	for _, opt := range opts {
		opt(c)
	}

	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if err, ok := rec.(error); ok && errors.Is(err, errBlocked) {
			commands = c.commands
			failure = nil
			return
		}
		var nd *nonDeterminismError
		if err, ok := rec.(error); ok && errors.As(err, &nd) {
			commands = nil
			failure = &Failure{Message: nd.Error(), NonDeterminism: true}
			return
		}
		commands = nil
		failure = &Failure{Message: fmt.Sprint(rec), NonDeterminism: false}
	}()

	out, callErr := fn(c, input)
	if seq, ok := c.unconsumedSeq(); ok {
		return nil, &Failure{
			Message:        fmt.Sprintf("non-determinism: history has scheduled command %d that the workflow did not reach", seq),
			NonDeterminism: true,
		}
	}
	if callErr != nil {
		c.commands = append(c.commands, FailWorkflow{Error: callErr.Error()})
		return c.commands, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, &Failure{Message: fmt.Errorf("encode workflow result: %w", err).Error()}
	}
	c.commands = append(c.commands, CompleteWorkflow{Result: raw})
	return c.commands, nil
}

func newContext(workflowID string, history []HistoryEvent) (*wfContext, error) {
	events := append([]HistoryEvent(nil), history...)
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })

	c := &wfContext{
		workflowID:  workflowID,
		scheduled:   map[int]scheduledCmd{},
		done:        map[int]commandDone{},
		commandTime: map[int]time.Time{},
		visited:     map[int]bool{},
	}
	for _, ev := range events {
		if err := c.indexEvent(ev); err != nil {
			return nil, err
		}
	}
	c.replaying = c.lastScheduledSeq > 0
	return c, nil
}

func (c *wfContext) indexEvent(ev HistoryEvent) error {
	switch ev.Type {
	case eventWorkflowStarted:
		if ev.CreatedAt.After(c.now) {
			c.now = ev.CreatedAt
		}
		return nil
	case eventActivityScheduled:
		var data struct {
			CommandSeq int    `json:"command_seq"`
			Name       string `json:"name"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if err := requireCommandSeq(ev.Type, data.CommandSeq); err != nil {
			return err
		}
		c.scheduled[data.CommandSeq] = scheduledCmd{Kind: "activity", Name: data.Name}
		c.note(data.CommandSeq, ev.CreatedAt)
	case eventActivityCompleted:
		var data struct {
			CommandSeq int             `json:"command_seq"`
			Result     json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if err := requireCommandSeq(ev.Type, data.CommandSeq); err != nil {
			return err
		}
		c.done[data.CommandSeq] = commandDone{Result: data.Result}
		c.note(data.CommandSeq, ev.CreatedAt)
	case eventActivityFailed:
		var data struct {
			CommandSeq   int    `json:"command_seq"`
			Error        string `json:"error"`
			NonRetryable bool   `json:"non_retryable"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if err := requireCommandSeq(ev.Type, data.CommandSeq); err != nil {
			return err
		}
		c.done[data.CommandSeq] = commandDone{Failed: true, Error: data.Error, NonRetryable: data.NonRetryable}
		c.note(data.CommandSeq, ev.CreatedAt)
	case eventTimerStarted:
		var data struct {
			CommandSeq int       `json:"command_seq"`
			FireAt     time.Time `json:"fire_at"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if err := requireCommandSeq(ev.Type, data.CommandSeq); err != nil {
			return err
		}
		c.scheduled[data.CommandSeq] = scheduledCmd{Kind: "timer", FireAt: data.FireAt}
		c.note(data.CommandSeq, ev.CreatedAt)
	case eventTimerFired:
		var data struct {
			CommandSeq int `json:"command_seq"`
		}
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			return fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if err := requireCommandSeq(ev.Type, data.CommandSeq); err != nil {
			return err
		}
		done := c.done[data.CommandSeq]
		done.TimerFired = true
		c.done[data.CommandSeq] = done
		c.note(data.CommandSeq, ev.CreatedAt)
	default:
		return nil
	}
	return nil
}

func (c *wfContext) note(seq int, at time.Time) {
	if seq > c.lastScheduledSeq && c.scheduled[seq].Kind != "" {
		c.lastScheduledSeq = seq
	}
	if prev, ok := c.commandTime[seq]; !ok || at.After(prev) {
		c.commandTime[seq] = at
	}
}

func requireCommandSeq(eventType string, seq int) error {
	if seq < 1 {
		return fmt.Errorf("%s missing command_seq", eventType)
	}
	return nil
}
