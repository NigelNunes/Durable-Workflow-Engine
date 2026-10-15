package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("demo services stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	latency := 200 * time.Millisecond
	if raw := os.Getenv("LATENCY"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("LATENCY: %w", err)
		}
		if d < 0 {
			return fmt.Errorf("LATENCY must not be negative")
		}
		latency = d
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	srv := &server{pool: pool, latency: latency}
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("demo services listening", "addr", httpSrv.Addr)
	return httpSrv.ListenAndServe()
}

type server struct {
	pool    *pgxpool.Pool
	latency time.Duration

	mu         sync.Mutex
	failing    bool
	creditDown bool
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /payments/charge", s.charge)
	mux.HandleFunc("POST /inventory/reserve", s.reserve)
	mux.HandleFunc("POST /email/send", s.send)
	mux.HandleFunc("POST /accounts", s.createAccount)
	mux.HandleFunc("POST /accounts/debit", s.debit)
	mux.HandleFunc("POST /accounts/credit", s.credit)
	mux.HandleFunc("POST /accounts/refund", s.refund)
	mux.HandleFunc("POST /reports/step", s.reportStep)
	mux.HandleFunc("POST /admin/payments/fail", s.setFailing)
	mux.HandleFunc("GET /admin/payments/fail", s.getFailing)
	mux.HandleFunc("POST /admin/credit/fail", s.setCreditFailing)
	mux.HandleFunc("GET /admin/credit/fail", s.getCreditFail)
	mux.HandleFunc("GET /stats", s.stats)
	return mux
}

func (s *server) charge(w http.ResponseWriter, r *http.Request) {
	s.pause()
	if s.paymentsFailing() {
		writeError(w, http.StatusServiceUnavailable, "payments down")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		OrderID     string `json:"order_id"`
		AmountCents int    `json:"amount_cents"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.OrderID == "" || body.AmountCents < 0 {
		writeError(w, http.StatusBadRequest, "order_id and amount_cents are required")
		return
	}
	result, err := charge(r.Context(), s.pool, key, body.OrderID, body.AmountCents)
	if err != nil {
		slog.Error("charge", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"charge_id":    result.ChargeID,
		"deduplicated": result.Deduplicated,
	})
}

func (s *server) reserve(w http.ResponseWriter, r *http.Request) {
	s.pause()
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		OrderID string `json:"order_id"`
		SKU     string `json:"sku"`
		Qty     int    `json:"qty"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.OrderID == "" || body.SKU == "" || body.Qty < 1 {
		writeError(w, http.StatusBadRequest, "order_id, sku, and qty are required")
		return
	}
	in := reservation{OrderID: body.OrderID, SKU: body.SKU, Qty: body.Qty}
	out, err := reserve(r.Context(), s.pool, key, in)
	if errors.Is(err, errRetryReserve) {
		out, err = reserve(r.Context(), s.pool, key, in)
	}
	if errors.Is(err, errNoStock) {
		writeError(w, http.StatusConflict, "not enough stock")
		return
	}
	if err != nil {
		slog.Error("reserve", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id": out.OrderID,
		"sku":      out.SKU,
		"qty":      out.Qty,
	})
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	s.pause()
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		OrderID  string `json:"order_id"`
		To       string `json:"to"`
		Template string `json:"template"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.OrderID == "" || body.To == "" || body.Template == "" {
		writeError(w, http.StatusBadRequest, "order_id, to, and template are required")
		return
	}
	in := emailSend{OrderID: body.OrderID, To: body.To, Template: body.Template}
	out, err := sendEmail(r.Context(), s.pool, key, in)
	if errors.Is(err, errRetryEmail) {
		out, err = sendEmail(r.Context(), s.pool, key, in)
	}
	if err != nil {
		slog.Error("send email", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"order_id": out.OrderID,
		"to":       out.To,
		"template": out.Template,
	})
}

func (s *server) setFailing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.mu.Lock()
	s.failing = body.Enabled
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getFailing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.paymentsFailing()})
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	out, err := loadStats(r.Context(), s.pool)
	if err != nil {
		slog.Error("stats", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAccount(w http.ResponseWriter, r *http.Request) {
	s.pause()
	var body struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	out, err := createAccount(r.Context(), s.pool, body.ID)
	if err != nil {
		slog.Error("create account", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) debit(w http.ResponseWriter, r *http.Request) {
	s.move(w, r, "debit")
}

func (s *server) credit(w http.ResponseWriter, r *http.Request) {
	s.pause()
	if s.creditFailing() {
		writeError(w, http.StatusConflict, "credit failed")
		return
	}
	s.move(w, r, "credit")
}

func (s *server) refund(w http.ResponseWriter, r *http.Request) {
	s.move(w, r, "refund")
}

func (s *server) move(w http.ResponseWriter, r *http.Request, kind string) {
	if kind != "credit" {
		s.pause()
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		AccountID   string `json:"account_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AccountID == "" || body.AmountCents < 1 {
		writeError(w, http.StatusBadRequest, "account_id and amount_cents are required")
		return
	}
	out, err := applyTransfer(r.Context(), s.pool, key, body.AccountID, kind, body.AmountCents)
	if errors.Is(err, errRetryTransfer) {
		out, err = applyTransfer(r.Context(), s.pool, key, body.AccountID, kind, body.AmountCents)
	}
	if errors.Is(err, errNoFunds) {
		writeError(w, http.StatusConflict, "not enough money")
		return
	}
	if err != nil {
		slog.Error("transfer", "kind", kind, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) reportStep(w http.ResponseWriter, r *http.Request) {
	s.pause()
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body reportStep
	if !decode(w, r, &body) {
		return
	}
	if body.ReportID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "report_id and name are required")
		return
	}
	out, err := recordReportStep(r.Context(), s.pool, key, body)
	if errors.Is(err, errRetryReport) {
		out, err = recordReportStep(r.Context(), s.pool, key, body)
	}
	if err != nil {
		slog.Error("report step", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setCreditFailing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.mu.Lock()
	s.creditDown = body.Enabled
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getCreditFail(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.creditFailing()})
}

func (s *server) creditFailing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creditDown
}

func (s *server) paymentsFailing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failing
}

func (s *server) pause() {
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required")
		return "", false
	}
	return key, true
}

func decode(w http.ResponseWriter, r *http.Request, dest any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
