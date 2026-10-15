// Package transfer is the money-transfer workflow. It imports the SDK only.
package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

// Input moves amount_cents from one demo account to another.
// PaceSeconds is a durable pause between debit and credit (0 skips it).
type Input struct {
	TransferID  string `json:"transfer_id"`
	FromAccount string `json:"from_account"`
	ToAccount   string `json:"to_account"`
	AmountCents int64  `json:"amount_cents"`
	PaceSeconds int    `json:"pace_seconds"`
}

// Result is transferred or rolled_back.
type Result struct {
	Status string `json:"status"`
}

// MoveIn is one debit, credit, or refund.
type MoveIn struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
}

// MoveOut is the stored transfer log row.
type MoveOut struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
	Kind        string `json:"kind"`
	DeltaCents  int64  `json:"delta_cents"`
}

// Workflow debits the source, then credits the destination.
// A terminal credit failure refunds the source.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	retry := workflow.ActivityOptions{Retry: workflow.RetryPolicy{
		MaxAttempts:        5,
		InitialInterval:    500 * time.Millisecond,
		BackoffCoefficient: 2,
		MaxInterval:        10 * time.Second,
	}}
	if _, err := workflow.Activity[MoveIn, MoveOut](ctx, "Debit", MoveIn{AccountID: in.FromAccount, AmountCents: in.AmountCents}, retry); err != nil {
		return Result{}, err
	}
	if in.PaceSeconds > 0 {
		if err := ctx.Sleep(time.Duration(in.PaceSeconds) * time.Second); err != nil {
			return Result{}, err
		}
	}
	if _, err := workflow.Activity[MoveIn, MoveOut](ctx, "Credit", MoveIn{AccountID: in.ToAccount, AmountCents: in.AmountCents}, retry); err != nil {
		if _, rerr := workflow.Activity[MoveIn, MoveOut](ctx, "Refund", MoveIn{AccountID: in.FromAccount, AmountCents: in.AmountCents}, retry); rerr != nil {
			return Result{Status: "refund_failed"}, rerr
		}
		return Result{Status: "rolled_back"}, nil
	}
	return Result{Status: "transferred"}, nil
}

// Debit removes amount_cents from an account.
func Debit(ctx context.Context, in MoveIn) (MoveOut, error) {
	return move(ctx, "/accounts/debit", in)
}

// Credit adds amount_cents to an account.
func Credit(ctx context.Context, in MoveIn) (MoveOut, error) {
	return move(ctx, "/accounts/credit", in)
}

// Refund returns amount_cents to an account after a failed credit.
func Refund(ctx context.Context, in MoveIn) (MoveOut, error) {
	return move(ctx, "/accounts/refund", in)
}

func move(ctx context.Context, path string, in MoveIn) (MoveOut, error) {
	var out MoveOut
	if err := post(ctx, path, in, &out); err != nil {
		return MoveOut{}, err
	}
	return out, nil
}

func post(ctx context.Context, path string, body any, dest any) error {
	base := strings.TrimRight(os.Getenv("SERVICES_URL"), "/")
	if base == "" {
		return fmt.Errorf("SERVICES_URL is required")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", activity.GetInfo(ctx).IdempotencyKey)
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer res.Body.Close()
	resp, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if res.StatusCode == http.StatusConflict {
		return workflow.NonRetryable(fmt.Errorf("%s: %s", path, resp))
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: http %d: %s", path, res.StatusCode, resp)
	}
	if dest != nil && len(resp) > 0 {
		if err := json.Unmarshal(resp, dest); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}
