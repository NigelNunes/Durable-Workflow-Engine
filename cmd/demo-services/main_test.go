package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(m *testing.M) {
	if os.Getenv("DATABASE_URL") == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := exec.Command("go", "run", "./cmd/migrate")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+os.Getenv("DATABASE_URL"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func TestChargeDedupesSameKey(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	srv := httptest.NewServer((&server{pool: pool}).routes())
	t.Cleanup(srv.Close)

	const key = "test-charge-dedupe"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM demo.charge_requests WHERE idempotency_key = $1`, key)
		_, _ = pool.Exec(context.Background(), `DELETE FROM demo.charges WHERE idempotency_key = $1`, key)
	})

	first := postCharge(t, srv.URL, key)
	second := postCharge(t, srv.URL, key)
	if first.ChargeID == 0 || second.ChargeID != first.ChargeID {
		t.Fatalf("ids %d and %d", first.ChargeID, second.ChargeID)
	}
	if first.Deduplicated || !second.Deduplicated {
		t.Fatalf("dedup flags %v %v", first.Deduplicated, second.Deduplicated)
	}

	var charges, blocked int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM demo.charges WHERE idempotency_key = $1
	`, key).Scan(&charges); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM demo.charge_requests WHERE idempotency_key = $1 AND deduplicated
	`, key).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if charges != 1 || blocked != 1 {
		t.Fatalf("charges %d blocked %d", charges, blocked)
	}

	stats := getStats(t, srv.URL)
	if stats.DuplicateCharges != 0 {
		t.Fatalf("duplicate_charges %d", stats.DuplicateCharges)
	}
}

type chargeBody struct {
	ChargeID     int64 `json:"charge_id"`
	Deduplicated bool  `json:"deduplicated"`
}

func postCharge(t *testing.T, base, key string) chargeBody {
	t.Helper()
	body := bytes.NewBufferString(`{"order_id":"test-order","amount_cents":2500}`)
	req, err := http.NewRequest(http.MethodPost, base+"/payments/charge", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out chargeBody
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getStats(t *testing.T, base string) stats {
	t.Helper()
	res, err := http.Get(base + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out stats
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
