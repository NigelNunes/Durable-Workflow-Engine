// Package activity carries the identity of the activity a worker is running.
package activity

import "context"

// Info identifies one activity attempt. The idempotency key is workflowID:commandSeq.
type Info struct {
	WorkflowID     string
	CommandSeq     int
	Attempt        int
	IdempotencyKey string
}

type infoKey struct{}

// WithInfo returns a context the activity function can read with GetInfo.
func WithInfo(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, infoKey{}, info)
}

// GetInfo returns the activity identity stored by the worker.
// A context without that identity returns the zero Info.
func GetInfo(ctx context.Context) Info {
	info, _ := ctx.Value(infoKey{}).(Info)
	return info
}
