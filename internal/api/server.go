package api

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

// Config is the HTTP server's view of process settings.
type Config struct {
	DemoKey           string
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
}

// NewHandler serves the engine HTTP API. Every route except GET /healthz
// requires the X-Demo-Key header to match cfg.DemoKey.
func NewHandler(st *store.Store, cfg Config) http.Handler {
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 10 * time.Second
	}
	cfg.LeaseTTL = store.NormalizeLeaseTTL(cfg.LeaseTTL)
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 3 * time.Second
	}
	s := &server{
		store:             st,
		leaseTTL:          cfg.LeaseTTL,
		heartbeatInterval: cfg.HeartbeatInterval,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/workflows", s.startWorkflow)
	mux.HandleFunc("GET /v1/workflows", s.listWorkflows)
	mux.HandleFunc("GET /v1/workflows/{id}/events", s.getEvents)
	mux.HandleFunc("GET /v1/workflows/{id}", s.getWorkflow)
	mux.HandleFunc("POST /v1/workers/register", s.registerWorker)
	mux.HandleFunc("POST /v1/workers/{id}/heartbeat", s.heartbeatWorker)
	mux.HandleFunc("POST /v1/tasks/poll", s.pollTasks)
	mux.HandleFunc("POST /v1/tasks/{id}/heartbeat", s.heartbeatTask)
	return requireDemoKey(cfg.DemoKey, mux)
}

type server struct {
	store             *store.Store
	leaseTTL          time.Duration
	heartbeatInterval time.Duration
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func requireDemoKey(demoKey string, next http.Handler) http.Handler {
	want := []byte(demoKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(r.Header.Get("X-Demo-Key"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid demo key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
