package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

type commandBody struct {
	Type        string          `json:"type"`
	Seq         int             `json:"seq"`
	Name        string          `json:"name"`
	Input       json.RawMessage `json:"input"`
	RetryPolicy json.RawMessage `json:"retry_policy"`
	Result      json.RawMessage `json:"result"`
	Error       string          `json:"error"`
	FireAt      time.Time       `json:"fire_at"`
}

type completeWorkflowBody struct {
	LeaseToken int64         `json:"lease_token"`
	WorkerID   string        `json:"worker_id"`
	Commands   []commandBody `json:"commands"`
}

type completeActivityBody struct {
	LeaseToken int64           `json:"lease_token"`
	WorkerID   string          `json:"worker_id"`
	Result     json.RawMessage `json:"result"`
}

type failActivityBody struct {
	LeaseToken   int64  `json:"lease_token"`
	WorkerID     string `json:"worker_id"`
	Error        string `json:"error"`
	NonRetryable bool   `json:"non_retryable"`
}

type failWorkflowBody struct {
	LeaseToken     int64  `json:"lease_token"`
	WorkerID       string `json:"worker_id"`
	Error          string `json:"error"`
	NonDeterminism bool   `json:"non_determinism"`
}

func (s *server) completeWorkflowTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := taskID(w, r)
	if !ok {
		return
	}
	var req completeWorkflowBody
	if !decodeBody(w, r, &req) {
		return
	}
	commands := make([]store.WorkflowCommand, 0, len(req.Commands))
	for _, cmd := range req.Commands {
		if !validJSON(cmd.Input) || !validJSON(cmd.Result) || !validJSON(cmd.RetryPolicy) {
			writeError(w, http.StatusBadRequest, "bad_request", "command JSON is invalid")
			return
		}
		commands = append(commands, store.WorkflowCommand{
			Type:        cmd.Type,
			Seq:         cmd.Seq,
			Name:        cmd.Name,
			Input:       cmd.Input,
			RetryPolicy: cmd.RetryPolicy,
			MaxAttempts: maxAttempts(cmd.RetryPolicy),
			Result:      cmd.Result,
			Error:       cmd.Error,
			FireAt:      cmd.FireAt,
		})
	}
	err := s.store.CompleteWorkflowTask(r.Context(), taskID, req.LeaseToken, req.WorkerID, commands)
	if writeCompleteErr(w, "complete workflow task", err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) completeActivityTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := taskID(w, r)
	if !ok {
		return
	}
	var req completeActivityBody
	if !decodeBody(w, r, &req) {
		return
	}
	if !validJSON(req.Result) {
		writeError(w, http.StatusBadRequest, "bad_request", "result JSON is invalid")
		return
	}
	err := s.store.CompleteActivityTask(r.Context(), taskID, req.LeaseToken, req.WorkerID, req.Result)
	if writeCompleteErr(w, "complete activity", err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) failActivityTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := taskID(w, r)
	if !ok {
		return
	}
	var req failActivityBody
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.store.FailActivityTask(r.Context(), taskID, req.LeaseToken, req.WorkerID, req.Error, req.NonRetryable)
	if writeCompleteErr(w, "fail activity", err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) failWorkflowTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := taskID(w, r)
	if !ok {
		return
	}
	var req failWorkflowBody
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.store.FailWorkflowTask(r.Context(), taskID, req.LeaseToken, req.WorkerID, req.Error, req.NonDeterminism)
	if writeCompleteErr(w, "fail workflow task", err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func taskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid task id")
		return 0, false
	}
	return id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

func validJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || json.Valid(raw)
}

func maxAttempts(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 1
	}
	var policy struct {
		MaxAttempts int `json:"max_attempts"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil || policy.MaxAttempts < 1 {
		return 1
	}
	return policy.MaxAttempts
}

func writeCompleteErr(w http.ResponseWriter, op string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrLeaseMismatch) {
		writeError(w, http.StatusConflict, "lease_mismatch", "lease token mismatch")
		return true
	}
	if errors.Is(err, store.ErrUnsupportedCommand) {
		writeError(w, http.StatusBadRequest, "bad_request", "command is not accepted")
		return true
	}
	if errors.Is(err, store.ErrBadCommand) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid command")
		return true
	}
	slog.Error(op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
	return true
}
