// Package workflow is the public API for deterministic workflow code.
// Workflow functions run from the top on every task. Activity and Sleep
// replay recorded results or emit one new command and stop.
package workflow

import (
	"encoding/json"
	"time"
)

// Context is the only handle workflow code has on the engine.
// Its clock, ids, and logs are replay-safe.
type Context interface {
	WorkflowID() string
	Now() time.Time
	UUID() string
	Logger() Logger
	Sleep(d time.Duration) error
}

// Logger records workflow logs. Lines from already-recorded history are dropped.
type Logger interface {
	Info(msg string, args ...any)
}

// ActivityOptions configures one activity command.
type ActivityOptions struct {
	Retry RetryPolicy
}

// RetryPolicy is stored with ScheduleActivity for the engine to apply later.
type RetryPolicy struct {
	MaxAttempts        int           `json:"max_attempts"`
	InitialInterval    time.Duration `json:"initial_interval"`
	BackoffCoefficient float64       `json:"backoff_coefficient"`
	MaxInterval        time.Duration `json:"max_interval"`
}

// ApplicationError is a recorded activity failure returned to workflow code.
type ApplicationError struct {
	Type         string
	Message      string
	NonRetryable bool
}

func (e *ApplicationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NonRetryable marks err as a terminal activity failure.
// The engine does not schedule another attempt.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &ApplicationError{Message: err.Error(), NonRetryable: true}
}

// Command is one instruction emitted by a workflow task.
type Command interface {
	isCommand()
}

// ScheduleActivity asks the engine to run an activity.
type ScheduleActivity struct {
	Seq   int             `json:"seq"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Retry RetryPolicy     `json:"retry_policy"`
}

func (ScheduleActivity) isCommand() {}

// StartTimer asks the engine to fire a durable timer.
type StartTimer struct {
	Seq    int       `json:"seq"`
	FireAt time.Time `json:"fire_at"`
}

func (StartTimer) isCommand() {}

// CompleteWorkflow marks a successful workflow return.
type CompleteWorkflow struct {
	Result json.RawMessage `json:"result"`
}

func (CompleteWorkflow) isCommand() {}

// FailWorkflow marks an error returned by workflow code.
type FailWorkflow struct {
	Error string `json:"error"`
}

func (FailWorkflow) isCommand() {}

// Failure is a problem with the workflow task itself.
// NonDeterminism means the code no longer matches history.
// The runner reports these instead of emitting FailWorkflow.
// A worker sends them to fail-workflow so the workflow stops.
type Failure struct {
	Message        string
	NonDeterminism bool
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	return f.Message
}

// HistoryEvent is one recorded event. Informational events are ignored.
type HistoryEvent struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// Option configures one run of a workflow task.
type Option func(*wfContext)

// WithLogger sets the logger used when replay is not replaying recorded commands.
func WithLogger(l Logger) Option {
	return func(c *wfContext) {
		c.logger = l
	}
}
