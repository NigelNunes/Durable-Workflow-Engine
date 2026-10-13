package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type engineClient struct {
	base string
	key  string
	http *http.Client
}

type statusError struct {
	Status int
	Body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Body)
}

func (c *engineClient) post(ctx context.Context, path string, body any, dest any) (int, error) {
	return c.do(ctx, http.MethodPost, path, body, dest)
}

func (c *engineClient) do(ctx context.Context, method, path string, body any, dest any) (int, error) {
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
		var httpErr *statusError
		if errors.As(err, &httpErr) || ctx.Err() != nil {
			return status, err
		}
		last = err
	}
	return 0, fmt.Errorf("%s %s: %w", method, path, last)
}

func (c *engineClient) once(ctx context.Context, method, path string, payload []byte, dest any) (int, error) {
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
		return res.StatusCode, &statusError{Status: res.StatusCode, Body: string(raw)}
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
