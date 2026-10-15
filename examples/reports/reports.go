// Package reports is the report-pipeline workflow. It imports the SDK only.
package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

// Input is one report to build. PaceSeconds is a durable pause between steps
// (0 skips the pause).
type Input struct {
	ReportID    string `json:"report_id"`
	PaceSeconds int    `json:"pace_seconds"`
}

// Result is the workflow return value.
type Result struct {
	Status string `json:"status"`
}

// FetchIn is the FetchData activity input.
type FetchIn struct {
	ReportID string `json:"report_id"`
}

// FetchOut is the fetched row count.
type FetchOut struct {
	Rows int `json:"rows"`
}

// StepIn is one stable report step.
type StepIn struct {
	ReportID string `json:"report_id"`
	Name     string `json:"name"`
}

// Workflow fetches data, then processes, uploads, and notifies.
// FetchData fails some attempts so the history shows retries.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	retry := workflow.ActivityOptions{Retry: workflow.RetryPolicy{
		MaxAttempts:        5,
		InitialInterval:    500 * time.Millisecond,
		BackoffCoefficient: 2,
		MaxInterval:        10 * time.Second,
	}}
	if _, err := workflow.Activity[FetchIn, FetchOut](ctx, "FetchData", FetchIn{ReportID: in.ReportID}, retry); err != nil {
		return Result{}, err
	}
	if err := pace(ctx, in.PaceSeconds); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[StepIn, struct{}](ctx, "Process", StepIn{ReportID: in.ReportID, Name: "process"}, retry); err != nil {
		return Result{}, err
	}
	if err := pace(ctx, in.PaceSeconds); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[StepIn, struct{}](ctx, "Upload", StepIn{ReportID: in.ReportID, Name: "upload"}, retry); err != nil {
		return Result{}, err
	}
	if err := pace(ctx, in.PaceSeconds); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[StepIn, struct{}](ctx, "Notify", StepIn{ReportID: in.ReportID, Name: "notify"}, retry); err != nil {
		return Result{}, err
	}
	return Result{Status: "completed"}, nil
}

func pace(ctx workflow.Context, seconds int) error {
	if seconds <= 0 {
		return nil
	}
	return ctx.Sleep(time.Duration(seconds) * time.Second)
}

// FetchData fails about 40 percent of attempts. The recorded result is replayed,
// so this randomness never runs inside workflow code.
func FetchData(ctx context.Context, in FetchIn) (FetchOut, error) {
	if err := ctx.Err(); err != nil {
		return FetchOut{}, err
	}
	if in.ReportID == "" {
		return FetchOut{}, fmt.Errorf("report_id is required")
	}
	if rand.Float64() < 0.4 {
		return FetchOut{}, fmt.Errorf("fetch failed")
	}
	return FetchOut{Rows: 3}, nil
}

// Process records the process step.
func Process(ctx context.Context, in StepIn) (struct{}, error) {
	return step(ctx, in)
}

// Upload records the upload step.
func Upload(ctx context.Context, in StepIn) (struct{}, error) {
	return step(ctx, in)
}

// Notify records the notify step.
func Notify(ctx context.Context, in StepIn) (struct{}, error) {
	return step(ctx, in)
}

func step(ctx context.Context, in StepIn) (struct{}, error) {
	if err := post(ctx, "/reports/step", in, nil); err != nil {
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
