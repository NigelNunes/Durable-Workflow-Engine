// Package store applies SQL migrations and, in later phases, talks to Postgres.
package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Result reports which migration files ran and which were already recorded.
type Result struct {
	Applied []string
	Skipped []string
}

// Migrate applies each *.sql file in migrations once, in filename order.
// A file already recorded in schema_migrations is left untouched, so a second
// run changes nothing.
func Migrate(ctx context.Context, databaseURL string, migrations fs.FS) (Result, error) {
	var result Result

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return result, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return result, err
	}

	names, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return result, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		applied, err := applyOne(ctx, conn, migrations, name, version)
		if err != nil {
			return result, err
		}
		if applied {
			result.Applied = append(result.Applied, version)
		} else {
			result.Skipped = append(result.Skipped, version)
		}
	}
	return result, nil
}

func ensureMigrationsTable(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, migrations fs.FS, name, version string) (bool, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	// One migrator at a time. A second process waits, then sees the recorded version.
	if _, err := tx.Exec(ctx, `LOCK TABLE schema_migrations IN EXCLUSIVE MODE`); err != nil {
		return false, fmt.Errorf("lock schema_migrations: %w", err)
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		version,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check %s: %w", name, err)
	}
	if exists {
		return false, nil
	}

	body, err := fs.ReadFile(migrations, name)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}
	if err := execScript(ctx, tx, string(body)); err != nil {
		return false, fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return false, fmt.Errorf("record %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit %s: %w", name, err)
	}
	return true, nil
}

// execScript runs a multi-statement SQL file inside the open transaction.
// pgx's extended protocol accepts only one statement, so this uses the simple protocol.
func execScript(ctx context.Context, tx pgx.Tx, script string) error {
	if _, err := tx.Conn().PgConn().Exec(ctx, script).ReadAll(); err != nil {
		return err
	}
	return nil
}
