// Package onboarding is the user-onboarding workflow. It imports the SDK only.
package onboarding

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

// Input is one user to onboard. DelaySeconds is the durable wait between emails.
type Input struct {
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
	DelaySeconds int    `json:"delay_seconds"`
}

// Result is the workflow return value.
type Result struct {
	Status string `json:"status"`
}

// AccountIn is the CreateAccount activity input.
type AccountIn struct {
	ID string `json:"id"`
}

// AccountOut is the stored account.
type AccountOut struct {
	ID           string `json:"id"`
	BalanceCents int64  `json:"balance_cents"`
}

// EmailIn is one welcome or tips email.
type EmailIn struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Template string `json:"template"`
}

// Workflow creates an account, sends a welcome email, waits, then sends tips.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	retry := workflow.ActivityOptions{Retry: workflow.RetryPolicy{
		MaxAttempts:        5,
		InitialInterval:    500 * time.Millisecond,
		BackoffCoefficient: 2,
		MaxInterval:        10 * time.Second,
	}}
	if _, err := workflow.Activity[AccountIn, AccountOut](ctx, "CreateAccount", AccountIn{ID: in.UserID}, retry); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[EmailIn, struct{}](ctx, "SendEmail", EmailIn{UserID: in.UserID, Email: in.Email, Template: "welcome"}, retry); err != nil {
		return Result{}, err
	}
	if err := ctx.Sleep(time.Duration(in.DelaySeconds) * time.Second); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[EmailIn, struct{}](ctx, "SendEmail", EmailIn{UserID: in.UserID, Email: in.Email, Template: "tips"}, retry); err != nil {
		return Result{}, err
	}
	return Result{Status: "onboarded"}, nil
}

// CreateAccount inserts the user account if it does not exist yet.
func CreateAccount(ctx context.Context, in AccountIn) (AccountOut, error) {
	var out AccountOut
	if err := post(ctx, "/accounts", in, &out); err != nil {
		return AccountOut{}, err
	}
	return out, nil
}

// SendEmail sends one template to the user's address.
func SendEmail(ctx context.Context, in EmailIn) (struct{}, error) {
	body := struct {
		OrderID  string `json:"order_id"`
		To       string `json:"to"`
		Template string `json:"template"`
	}{OrderID: in.UserID, To: in.Email, Template: in.Template}
	if err := post(ctx, "/email/send", body, nil); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
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
