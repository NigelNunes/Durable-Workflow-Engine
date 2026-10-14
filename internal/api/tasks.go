package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

const maxPollWait = 20 * time.Second

type registerRequest struct {
	Name string `json:"name"`
}

type pollRequest struct {
	WorkerID string   `json:"worker_id"`
	Kinds    []string `json:"kinds"`
	WaitMS   *int     `json:"wait_ms"`
}

type taskHeartbeatRequest struct {
	LeaseToken int64 `json:"lease_token"`
}

func (s *server) registerWorker(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	id, err := s.store.RegisterWorker(r.Context(), req.Name)
	if err != nil {
		slog.Error("register worker", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"worker_id":             id,
		"lease_ttl_ms":          s.leaseTTL.Milliseconds(),
		"heartbeat_interval_ms": s.heartbeatInterval.Milliseconds(),
	})
}

func (s *server) heartbeatWorker(w http.ResponseWriter, r *http.Request) {
	chaos, err := s.store.HeartbeatWorker(r.Context(), r.PathValue("id"), s.leaseTTL)
	if errors.Is(err, store.ErrWorkerNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "worker not found")
		return
	}
	if err != nil {
		slog.Error("worker heartbeat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	var delivered any
	if chaos != "" {
		delivered = chaos
	}
	writeJSON(w, http.StatusOK, map[string]any{"chaos": delivered})
}

func (s *server) pollTasks(w http.ResponseWriter, r *http.Request) {
	var req pollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.WorkerID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "worker_id is required")
		return
	}
	if len(req.Kinds) == 0 || !validKinds(req.Kinds) {
		writeError(w, http.StatusBadRequest, "bad_request", "kinds must be workflow and/or activity")
		return
	}
	wait := maxPollWait
	if req.WaitMS != nil {
		if *req.WaitMS < 0 || *req.WaitMS > int(maxPollWait/time.Millisecond) {
			writeError(w, http.StatusBadRequest, "bad_request", "wait_ms must be between 0 and 20000")
			return
		}
		wait = time.Duration(*req.WaitMS) * time.Millisecond
	}

	task, ok, err := s.store.PollTask(r.Context(), req.WorkerID, req.Kinds, s.leaseTTL, wait)
	if errors.Is(err, store.ErrWorkerNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "worker not found")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if err != nil {
		slog.Error("poll tasks", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writePolledTask(w, task)
}

func (s *server) heartbeatTask(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid task id")
		return
	}
	var req taskHeartbeatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	err = s.store.HeartbeatTask(r.Context(), taskID, req.LeaseToken, s.leaseTTL)
	if errors.Is(err, store.ErrLeaseMismatch) {
		writeError(w, http.StatusConflict, "lease_mismatch", "lease token mismatch")
		return
	}
	if err != nil {
		slog.Error("task heartbeat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writePolledTask(w http.ResponseWriter, task store.PolledTask) {
	switch task.Kind {
	case "workflow":
		history := task.History
		if history == nil {
			history = []store.Event{}
		}
		writeJSON(w, http.StatusOK, struct {
			TaskID     int64                `json:"task_id"`
			Kind       string               `json:"kind"`
			LeaseToken int64                `json:"lease_token"`
			Workflow   store.PolledWorkflow `json:"workflow"`
			History    []store.Event        `json:"history"`
			HistoryLen int                  `json:"history_len"`
		}{
			TaskID:     task.TaskID,
			Kind:       task.Kind,
			LeaseToken: task.LeaseToken,
			Workflow:   task.Workflow,
			History:    history,
			HistoryLen: task.HistoryLen,
		})
	case "activity":
		writeJSON(w, http.StatusOK, struct {
			TaskID         int64           `json:"task_id"`
			Kind           string          `json:"kind"`
			LeaseToken     int64           `json:"lease_token"`
			WorkflowID     string          `json:"workflow_id"`
			CommandSeq     int             `json:"command_seq"`
			ActivityName   string          `json:"activity_name"`
			Input          json.RawMessage `json:"input"`
			Attempt        int             `json:"attempt"`
			IdempotencyKey string          `json:"idempotency_key"`
		}{
			TaskID:         task.TaskID,
			Kind:           task.Kind,
			LeaseToken:     task.LeaseToken,
			WorkflowID:     task.WorkflowID,
			CommandSeq:     task.CommandSeq,
			ActivityName:   task.ActivityName,
			Input:          task.Input,
			Attempt:        task.Attempt,
			IdempotencyKey: task.IdempotencyKey,
		})
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func validKinds(kinds []string) bool {
	for _, kind := range kinds {
		if kind != "workflow" && kind != "activity" {
			return false
		}
	}
	return true
}
