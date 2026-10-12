// Package history holds event and command names shared by the engine.
package history

const (
	WorkflowStarted          = "WorkflowStarted"
	ActivityScheduled        = "ActivityScheduled"
	ActivityLeaseExpired     = "ActivityLeaseExpired"
	WorkflowTaskLeaseExpired = "WorkflowTaskLeaseExpired"
)
