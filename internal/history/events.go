// Package history holds event and command names shared by the engine.
package history

const (
	WorkflowStarted          = "WorkflowStarted"
	ActivityScheduled        = "ActivityScheduled"
	ActivityCompleted        = "ActivityCompleted"
	ActivityFailed           = "ActivityFailed"
	WorkflowCompleted        = "WorkflowCompleted"
	WorkflowFailed           = "WorkflowFailed"
	ActivityRetryScheduled   = "ActivityRetryScheduled"
	ActivityLeaseExpired     = "ActivityLeaseExpired"
	WorkflowTaskLeaseExpired = "WorkflowTaskLeaseExpired"
	StaleCompletionRejected  = "StaleCompletionRejected"

	// TooManyRedeliveries is the terminal activity error after the redelivery cap.
	TooManyRedeliveries = "TooManyRedeliveries"

	CmdScheduleActivity = "ScheduleActivity"
	CmdStartTimer       = "StartTimer"
	CmdCompleteWorkflow = "CompleteWorkflow"
	CmdFailWorkflow     = "FailWorkflow"
)
