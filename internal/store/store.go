package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound is returned when a workflow id does not exist.
	ErrNotFound = errors.New("workflow not found")
	// ErrWorkerNotFound is returned when a worker id is not registered.
	ErrWorkerNotFound = errors.New("worker not found")
	// ErrLeaseMismatch is returned when a task heartbeat or completion presents a stale token.
	ErrLeaseMismatch = errors.New("lease token mismatch")
	// ErrBadCommand is returned when a completion command is missing required fields.
	ErrBadCommand = errors.New("bad command")
	// ErrUnsupportedCommand is returned when a command is not accepted yet.
	ErrUnsupportedCommand = errors.New("unsupported command")
)

// DefaultMaxRedeliveries is how many times a crashed activity is queued again
// before the engine fails it. Application retries do not use this counter.
const DefaultMaxRedeliveries = 20

// Config tunes the connection pool. Zero values keep the pgx defaults.
type Config struct {
	MaxConns int32
}

// Store talks to the engine tables in Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool. The caller must Close it.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	return Open(ctx, databaseURL, Config{})
}

// Open opens a connection pool with cfg. The caller must Close it.
func Open(ctx context.Context, databaseURL string, cfg Config) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
