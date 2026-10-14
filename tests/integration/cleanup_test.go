package integration

import (
	"context"
	"testing"
	"time"

	"github.com/nigelnunes/durable-flow/internal/store"
)

func TestDeleteOldCompleted(t *testing.T) {
	resetQueue(t)
	st, err := store.Open(context.Background(), dbURL, store.Config{MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO workflows (id, type, status, input, updated_at) VALUES
			('old-done', 'noop', 'completed', 'null', now() - interval '25 hours'),
			('new-done', 'noop', 'completed', 'null', now() - interval '1 hour'),
			('old-run', 'noop', 'running', 'null', now() - interval '25 hours'),
			('old-fail', 'noop', 'failed', 'null', now() - interval '25 hours')
	`); err != nil {
		t.Fatal(err)
	}
	n, err := st.DeleteOldCompleted(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted %d", n)
	}
	rows, err := pool.Query(context.Background(), `SELECT id FROM workflows ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "new-done" || ids[1] != "old-fail" || ids[2] != "old-run" {
		t.Fatalf("remaining %+v", ids)
	}
}
