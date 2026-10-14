package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

const (
	chaosCooldown   = 3 * time.Second
	restartCooldown = 30 * time.Second
	stallChaos      = "stall:15"
)

func (s *server) enforceDemoLimits(w http.ResponseWriter, r *http.Request, id string) error {
	if !s.demoMode {
		return nil
	}
	if id != "" {
		exists, err := s.store.HasWorkflow(r.Context(), id)
		if err != nil {
			slog.Error("check workflow", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
			return err
		}
		if exists {
			return nil
		}
	}
	if !s.starts.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many workflow starts")
		return errLimited
	}
	n, err := s.store.CountRunning(r.Context())
	if err != nil {
		slog.Error("count running workflows", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return err
	}
	if n >= s.maxRunning {
		writeError(w, http.StatusTooManyRequests, "too_many_workflows", "too many running workflows")
		return errLimited
	}
	return nil
}

var errLimited = errors.New("limited")

type chaosBody struct {
	WorkerID string `json:"worker_id"`
	Action   string `json:"action"`
}

func (s *server) adminChaos(w http.ResponseWriter, r *http.Request) {
	if !s.allowAdmin(w, r) {
		return
	}
	var req chaosBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	value, ok := chaosValue(req.Action)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "action must be crash or stall")
		return
	}
	if !s.claimCooldown(&s.lastChaos, chaosCooldown) {
		writeError(w, http.StatusTooManyRequests, "cooldown", "chaos is cooling down")
		return
	}
	id, err := s.store.ArmChaos(r.Context(), req.WorkerID, value, 3*s.heartbeatInterval)
	if err != nil {
		s.clearCooldown(&s.lastChaos)
	}
	if errors.Is(err, store.ErrWorkerNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "worker not found")
		return
	}
	if errors.Is(err, store.ErrNoAliveWorker) {
		writeError(w, http.StatusConflict, "no_alive_worker", "no alive worker")
		return
	}
	if err != nil {
		slog.Error("arm chaos", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"worker_id": id, "chaos": value})
}

func chaosValue(action string) (string, bool) {
	switch action {
	case "crash":
		return "crash", true
	case "stall":
		return stallChaos, true
	default:
		return "", false
	}
}

func (s *server) restartEngine(w http.ResponseWriter, r *http.Request) {
	if !s.allowAdmin(w, r) {
		return
	}
	if !s.claimCooldown(&s.lastRestart, restartCooldown) {
		writeError(w, http.StatusTooManyRequests, "cooldown", "restart is cooling down")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.exit(0)
	}()
}

func (s *server) allowAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.demoMode {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return false
	}
	got := []byte(r.Header.Get("X-Admin-Token"))
	if s.adminToken == "" || subtle.ConstantTimeCompare(got, []byte(s.adminToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid admin token")
		return false
	}
	return true
}

func (s *server) claimCooldown(last *time.Time, window time.Duration) bool {
	s.coolMu.Lock()
	defer s.coolMu.Unlock()
	now := time.Now()
	if !last.IsZero() && now.Sub(*last) < window {
		return false
	}
	*last = now
	return true
}

func (s *server) clearCooldown(last *time.Time) {
	s.coolMu.Lock()
	defer s.coolMu.Unlock()
	*last = time.Time{}
}

func (s *server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.store.ListWorkers(r.Context(), 3*s.heartbeatInterval)
	if err != nil {
		slog.Error("list workers", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
}

type dashboardWorker struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Alive        bool   `json:"alive"`
	RunningTasks int    `json:"running_tasks"`
}

func (s *server) dashboardState(w http.ResponseWriter, r *http.Request) {
	aliveFor := 3 * s.heartbeatInterval
	workers, err := s.store.ListWorkers(r.Context(), aliveFor)
	if err != nil {
		slog.Error("dashboard workers", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	workflows, err := s.store.DashboardWorkflows(r.Context(), 50)
	if err != nil {
		slog.Error("dashboard workflows", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	counters, err := s.store.DashboardCounters(r.Context())
	if err != nil {
		slog.Error("dashboard counters", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	view := make([]dashboardWorker, 0, len(workers))
	for _, worker := range workers {
		view = append(view, dashboardWorker{
			ID:           worker.ID,
			Name:         worker.Name,
			Alive:        worker.Alive,
			RunningTasks: worker.RunningTasks,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workers":   view,
		"workflows": workflows,
		"counters":  counters,
	})
}
