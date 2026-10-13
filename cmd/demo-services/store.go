package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type chargeResult struct {
	ChargeID     int64
	Deduplicated bool
}

type reservation struct {
	OrderID string
	SKU     string
	Qty     int
}

type emailSend struct {
	OrderID  string
	To       string
	Template string
}

type stats struct {
	DuplicateCharges         int `json:"duplicate_charges"`
	DuplicateRequestsBlocked int `json:"duplicate_requests_blocked"`
	TotalCharges             int `json:"total_charges"`
}

func charge(ctx context.Context, pool *pgxpool.Pool, key, orderID string, amountCents int) (chargeResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return chargeResult{}, fmt.Errorf("begin charge: %w", err)
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO demo.charges (idempotency_key, order_id, amount_cents)
		VALUES ($1, $2, $3)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id
	`, key, orderID, amountCents).Scan(&id)
	deduped := false
	if errors.Is(err, pgx.ErrNoRows) {
		deduped = true
		if err := tx.QueryRow(ctx, `SELECT id FROM demo.charges WHERE idempotency_key = $1`, key).Scan(&id); err != nil {
			return chargeResult{}, fmt.Errorf("load charge: %w", err)
		}
	} else if err != nil {
		return chargeResult{}, fmt.Errorf("insert charge: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo.charge_requests (idempotency_key, order_id, deduplicated)
		VALUES ($1, $2, $3)
	`, key, orderID, deduped); err != nil {
		return chargeResult{}, fmt.Errorf("insert charge request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return chargeResult{}, fmt.Errorf("commit charge: %w", err)
	}
	return chargeResult{ChargeID: id, Deduplicated: deduped}, nil
}

func reserve(ctx context.Context, pool *pgxpool.Pool, key string, in reservation) (reservation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return reservation{}, fmt.Errorf("begin reserve: %w", err)
	}
	defer tx.Rollback(ctx)

	existing, ok, err := loadReservation(ctx, tx, key)
	if err != nil {
		return reservation{}, err
	}
	if ok {
		if err := tx.Commit(ctx); err != nil {
			return reservation{}, fmt.Errorf("commit reserve: %w", err)
		}
		return existing, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE demo.inventory
		SET stock = stock - $2
		WHERE sku = $1 AND stock >= $2
	`, in.SKU, in.Qty)
	if err != nil {
		return reservation{}, fmt.Errorf("decrement stock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return reservation{}, errNoStock
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo.reservations (idempotency_key, order_id, sku, qty)
		VALUES ($1, $2, $3, $4)
	`, key, in.OrderID, in.SKU, in.Qty); err != nil {
		if isUnique(err) {
			return reservation{}, errRetryReserve
		}
		return reservation{}, fmt.Errorf("insert reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return reservation{}, fmt.Errorf("commit reserve: %w", err)
	}
	return in, nil
}

func loadReservation(ctx context.Context, tx pgx.Tx, key string) (reservation, bool, error) {
	var out reservation
	err := tx.QueryRow(ctx, `
		SELECT order_id, sku, qty FROM demo.reservations WHERE idempotency_key = $1
	`, key).Scan(&out.OrderID, &out.SKU, &out.Qty)
	if errors.Is(err, pgx.ErrNoRows) {
		return reservation{}, false, nil
	}
	if err != nil {
		return reservation{}, false, fmt.Errorf("load reservation: %w", err)
	}
	return out, true, nil
}

func sendEmail(ctx context.Context, pool *pgxpool.Pool, key string, in emailSend) (emailSend, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return emailSend{}, fmt.Errorf("begin email: %w", err)
	}
	defer tx.Rollback(ctx)

	var existing emailSend
	err = tx.QueryRow(ctx, `
		SELECT to_addr, template FROM demo.emails WHERE idempotency_key = $1
	`, key).Scan(&existing.To, &existing.Template)
	if err == nil {
		existing.OrderID = in.OrderID
		if err := tx.Commit(ctx); err != nil {
			return emailSend{}, fmt.Errorf("commit email: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return emailSend{}, fmt.Errorf("load email: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo.emails (idempotency_key, to_addr, template)
		VALUES ($1, $2, $3)
	`, key, in.To, in.Template); err != nil {
		if isUnique(err) {
			return emailSend{}, errRetryEmail
		}
		return emailSend{}, fmt.Errorf("insert email: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return emailSend{}, fmt.Errorf("commit email: %w", err)
	}
	return in, nil
}

func loadStats(ctx context.Context, pool *pgxpool.Pool) (stats, error) {
	var out stats
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT order_id
			FROM demo.charges
			GROUP BY order_id
			HAVING count(DISTINCT idempotency_key) > 1
		) dupes
	`).Scan(&out.DuplicateCharges); err != nil {
		return stats{}, fmt.Errorf("count duplicate charges: %w", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM demo.charge_requests WHERE deduplicated
	`).Scan(&out.DuplicateRequestsBlocked); err != nil {
		return stats{}, fmt.Errorf("count blocked requests: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM demo.charges`).Scan(&out.TotalCharges); err != nil {
		return stats{}, fmt.Errorf("count charges: %w", err)
	}
	return out, nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var (
	errNoStock      = errors.New("not enough stock")
	errRetryReserve = errors.New("reservation conflict")
	errRetryEmail   = errors.New("email conflict")
)
