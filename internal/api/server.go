package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/nigelnunes/durable-flow/internal/store"
)

// NewHandler serves the engine HTTP API. Every route except GET /healthz
// requires the X-Demo-Key header to match demoKey.
func NewHandler(st *store.Store, demoKey string) http.Handler {
	s := &server{store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/workflows", s.startWorkflow)
	mux.HandleFunc("GET /v1/workflows", s.listWorkflows)
	mux.HandleFunc("GET /v1/workflows/{id}/events", s.getEvents)
	mux.HandleFunc("GET /v1/workflows/{id}", s.getWorkflow)
	return requireDemoKey(demoKey, mux)
}

type server struct {
	store *store.Store
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
