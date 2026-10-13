// Command chaos-worker is the subprocess the chaos tests kill with SIGKILL.
// SCENARIO and ROLE select the crash behavior. Workflow code stays deterministic.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/worker"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

func main() {
	engineURL := os.Getenv("ENGINE_URL")
	demoKey := os.Getenv("DEMO_KEY")
	if engineURL == "" || demoKey == "" {
		fmt.Fprintln(os.Stderr, "ENGINE_URL and DEMO_KEY are required")
		os.Exit(2)
	}
	name := os.Getenv("WORKER_NAME")
	if name == "" {
		name = "chaos"
	}
	var pause atomic.Bool
	w := worker.New(worker.Options{
		Name:      name,
		EngineURL: engineURL,
		DemoKey:   demoKey,
		PauseHeartbeats: func() bool {
			return pause.Load()
		},
		BeforeWorkflowReplay: func(string) {
			if scenario() == "f3" && role() == "victim" {
				signalReady("workflow")
				time.Sleep(30 * time.Second)
			}
		},
	})
	register(w, &pause)
	if err := w.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func scenario() string {
	if s := os.Getenv("SCENARIO"); s != "" {
		return s
	}
	return "plain"
}

func role() string {
	if r := os.Getenv("ROLE"); r != "" {
		return r
	}
	return "other"
}

func version() string {
	if v := os.Getenv("WORKFLOW_VERSION"); v != "" {
		return v
	}
	return "1"
}

func register(w *worker.Worker, pause *atomic.Bool) {
	worker.RegisterWorkflow(w, "charge_once", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Charge", struct{}{}, workflow.ActivityOptions{})
	})
	worker.RegisterWorkflow(w, "replay_kill", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Touch", struct{}{}, workflow.ActivityOptions{})
	})
	worker.RegisterWorkflow(w, "stall", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Stall", struct{}{}, workflow.ActivityOptions{})
	})
	worker.RegisterWorkflow(w, "flaky", func(ctx workflow.Context, in flakyIn) (string, error) {
		attempts := in.MaxAttempts
		if attempts < 1 {
			attempts = 4
		}
		return workflow.Activity[struct{}, string](ctx, "Flaky", struct{}{}, workflow.ActivityOptions{
			Retry: workflow.RetryPolicy{
				MaxAttempts:        attempts,
				InitialInterval:    50 * time.Millisecond,
				BackoffCoefficient: 2,
				MaxInterval:        time.Second,
			},
		})
	})
	worker.RegisterWorkflow(w, "hold", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Hold", struct{}{}, workflow.ActivityOptions{})
	})
	worker.RegisterWorkflow(w, "sleeper", func(ctx workflow.Context, _ struct{}) (string, error) {
		if err := ctx.Sleep(time.Second); err != nil {
			return "", err
		}
		return "awake", nil
	})
	worker.RegisterWorkflow(w, "versioned", versionedWorkflow)
	worker.RegisterWorkflow(w, "poison", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Poison", struct{}{}, workflow.ActivityOptions{})
	})
	worker.RegisterWorkflow(w, "plain", func(ctx workflow.Context, _ struct{}) (string, error) {
		return workflow.Activity[struct{}, string](ctx, "Echo", struct{}{}, workflow.ActivityOptions{})
	})

	worker.RegisterActivity(w, "Charge", func(ctx context.Context, _ struct{}) (string, error) {
		return charge(ctx)
	})
	worker.RegisterActivity(w, "Touch", func(context.Context, struct{}) (string, error) {
		return "ok", nil
	})
	worker.RegisterActivity(w, "Stall", func(context.Context, struct{}) (string, error) {
		if role() == "victim" {
			pause.Store(true)
			signalReady("stalled")
			time.Sleep(3 * time.Second)
			return "stall", nil
		}
		return "other", nil
	})
	worker.RegisterActivity(w, "Flaky", func(context.Context, struct{}) (string, error) {
		if paymentsFailing() {
			return "", fmt.Errorf("payments down")
		}
		return "ok", nil
	})
	worker.RegisterActivity(w, "Hold", func(context.Context, struct{}) (string, error) {
		return hold(pause)
	})
	worker.RegisterActivity(w, "StepA", func(context.Context, struct{}) (string, error) {
		if scenario() == "f10" && role() == "victim" {
			signalReady("step-a")
			time.Sleep(30 * time.Second)
		}
		return "a", nil
	})
	worker.RegisterActivity(w, "StepB", func(context.Context, struct{}) (string, error) {
		return "b", nil
	})
	worker.RegisterActivity(w, "Poison", func(context.Context, struct{}) (string, error) {
		if scenario() == "f11" {
			os.Exit(1)
		}
		return "", fmt.Errorf("poison")
	})
	worker.RegisterActivity(w, "Echo", func(context.Context, struct{}) (string, error) {
		return "ok", nil
	})
}

type flakyIn struct {
	MaxAttempts int `json:"max_attempts"`
}

func versionedWorkflow(ctx workflow.Context, _ struct{}) (string, error) {
	opts := workflow.ActivityOptions{}
	if version() == "2" {
		if _, err := workflow.Activity[struct{}, string](ctx, "StepB", struct{}{}, opts); err != nil {
			return "", err
		}
		if _, err := workflow.Activity[struct{}, string](ctx, "StepA", struct{}{}, opts); err != nil {
			return "", err
		}
		return "v2", nil
	}
	if _, err := workflow.Activity[struct{}, string](ctx, "StepA", struct{}{}, opts); err != nil {
		return "", err
	}
	if _, err := workflow.Activity[struct{}, string](ctx, "StepB", struct{}{}, opts); err != nil {
		return "", err
	}
	return "v1", nil
}

func charge(ctx context.Context) (string, error) {
	key := activity.GetInfo(ctx).IdempotencyKey
	switch scenario() {
	case "f1":
		if role() == "victim" {
			signalReady("before-charge")
			time.Sleep(30 * time.Second)
		}
		return postCharge(key)
	case "f2":
		id, err := postCharge(key)
		if err != nil {
			return "", err
		}
		if role() == "victim" {
			signalReady("after-charge")
			time.Sleep(30 * time.Second)
		}
		return id, nil
	default:
		time.Sleep(150 * time.Millisecond)
		return postCharge(key)
	}
}

func hold(pause *atomic.Bool) (string, error) {
	switch scenario() {
	case "f9":
		if role() == "victim" {
			pause.Store(true)
			signalReady("holding")
			time.Sleep(2 * time.Second)
			return "from-a", nil
		}
		return "from-b", nil
	default:
		signalReady("holding")
		time.Sleep(2 * time.Second)
		return "done", nil
	}
}

func signalReady(phase string) {
	body, _ := json.Marshal(map[string]string{"phase": phase})
	req, err := http.NewRequest(http.MethodPost, os.Getenv("FAKE_URL")+"/ready", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	res.Body.Close()
}

func postCharge(key string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, os.Getenv("FAKE_URL")+"/charge", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("charge http %d: %s", res.StatusCode, raw)
	}
	var out struct {
		ChargeID string `json:"charge_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.ChargeID, nil
}

func paymentsFailing() bool {
	res, err := http.Get(os.Getenv("FAKE_URL") + "/failing")
	if err != nil {
		return true
	}
	defer res.Body.Close()
	var out struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return true
	}
	return out.Enabled
}
