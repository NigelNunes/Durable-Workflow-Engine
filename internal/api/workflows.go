package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

const maxWorkflowIDLen = 256

type startRequest struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Input json.RawMessage `json:"input"`
}

func (s *server) startWorkflow(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req startRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "type is required")
		return
	}
	if req.ID != "" && !validWorkflowID(req.ID) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid workflow id")
		return
	}

	id, err := s.store.StartWorkflow(r.Context(), req.ID, req.Type, req.Input)
	if err != nil {
		slog.Error("start workflow", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.ListFilter{
		Status: q.Get("status"),
		Type:   q.Get("type"),
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be between 1 and 200")
			return
		}
		filter.Limit = n
	}
	if raw := q.Get("before"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "before must be RFC3339")
			return
		}
		filter.Before = &t
	}

	workflows, err := s.store.ListWorkflows(r.Context(), filter)
	if err != nil {
		slog.Error("list workflows", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": workflows})
}

func (s *server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow, err := s.store.GetWorkflow(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "workflow not found")
		return
	}
	if err != nil {
		slog.Error("get workflow", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, workflow)
}

func (s *server) getEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.GetEvents(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "workflow not found")
		return
	}
	if err != nil {
		slog.Error("get events", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func validWorkflowID(id string) bool {
	if id == "" || len(id) > maxWorkflowIDLen {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == '/' {
			return false
		}
	}
	return true
}
