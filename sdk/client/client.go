// Package client starts workflows and reads them back from an engine.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the engine HTTP API.
type Client struct {
	base string
	key  string
	http *http.Client
}

// Workflow is the engine's view of one workflow.
type Workflow struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Input      json.RawMessage `json:"input"`
	Result     json.RawMessage `json:"result"`
	Error      *string         `json:"error"`
	HistoryLen int             `json:"history_len"`
}

// Event is one history row.
type Event struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// New returns a client for engineURL. demoKey is sent as X-Demo-Key.
func New(engineURL, demoKey string) *Client {
	return &Client{
		base: strings.TrimRight(engineURL, "/"),
		key:  demoKey,
		http: &http.Client{},
	}
}

// StartWorkflow inserts a workflow and returns its id.
// An empty id lets the engine generate one.
func (c *Client) StartWorkflow(ctx context.Context, workflowType string, input any, id string) (string, error) {
	raw, err := marshalInput(input)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"type":  workflowType,
		"input": raw,
	}
	if id != "" {
		body["id"] = id
	}
	var out struct {
		ID string `json:"id"`
	}
	if _, err := c.post(ctx, "/v1/workflows", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("start workflow: empty id")
	}
	return out.ID, nil
}

// GetWorkflow fetches one workflow.
func (c *Client) GetWorkflow(ctx context.Context, id string) (Workflow, error) {
	var wf Workflow
	if _, err := c.do(ctx, http.MethodGet, "/v1/workflows/"+id, nil, &wf); err != nil {
		return Workflow{}, err
	}
	return wf, nil
}

// GetHistory fetches the append-only event log.
func (c *Client) GetHistory(ctx context.Context, id string) ([]Event, error) {
	var events []Event
	if _, err := c.do(ctx, http.MethodGet, "/v1/workflows/"+id+"/events", nil, &events); err != nil {
		return nil, err
	}
	if events == nil {
		events = []Event{}
	}
	return events, nil
}

func marshalInput(input any) (json.RawMessage, error) {
	if input == nil {
		return json.RawMessage("null"), nil
	}
	if raw, ok := input.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage("null"), nil
		}
		return raw, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode workflow input: %w", err)
	}
	return encoded, nil
}

func (c *Client) post(ctx context.Context, path string, body any, dest any) (int, error) {
	return c.do(ctx, http.MethodPost, path, body, dest)
}

type httpStatus struct {
	status int
	body   string
}

func (e *httpStatus) Error() string {
	return fmt.Sprintf("http %d: %s", e.status, e.body)
}

func (c *Client) do(ctx context.Context, method, path string, body any, dest any) (int, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 && !sleep(ctx, time.Duration(attempt)*100*time.Millisecond) {
			return 0, ctx.Err()
		}
		status, err := c.once(ctx, method, path, payload, dest)
		if err == nil {
			return status, nil
		}
		var httpErr *httpStatus
		if errors.As(err, &httpErr) || ctx.Err() != nil {
			return status, err
		}
		last = err
	}
	return 0, fmt.Errorf("%s %s: %w", method, path, last)
}

func (c *Client) once(ctx context.Context, method, path string, payload []byte, dest any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Demo-Key", c.key)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 500 {
		return res.StatusCode, fmt.Errorf("http %d: %s", res.StatusCode, raw)
	}
	if res.StatusCode >= 400 {
		return res.StatusCode, &httpStatus{status: res.StatusCode, body: string(raw)}
	}
	if dest != nil && res.StatusCode != http.StatusNoContent && len(raw) > 0 {
		if err := json.Unmarshal(raw, dest); err != nil {
			return res.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return res.StatusCode, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
