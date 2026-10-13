// Package history holds event and command names shared by the engine.
package history

const (
	WorkflowStarted          = "WorkflowStarted"
	ActivityScheduled        = "ActivityScheduled"
	ActivityCompleted        = "ActivityCompleted"
	ActivityFailed           = "ActivityFailed"
	WorkflowCompleted        = "WorkflowCompleted"
	WorkflowFailed           = "WorkflowFailed"
	ActivityLeaseExpired     = "ActivityLeaseExpired"
	WorkflowTaskLeaseExpired = "WorkflowTaskLeaseExpired"

	CmdScheduleActivity = "ScheduleActivity"
	CmdStartTimer       = "StartTimer"
	CmdCompleteWorkflow = "CompleteWorkflow"
	CmdFailWorkflow     = "FailWorkflow"
)
