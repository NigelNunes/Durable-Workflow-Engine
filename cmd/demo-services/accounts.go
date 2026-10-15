package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type account struct {
	ID           string `json:"id"`
	BalanceCents int64  `json:"balance_cents"`
}

type movement struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
	Kind        string `json:"kind"`
	DeltaCents  int64  `json:"delta_cents"`
}

type reportStep struct {
	ReportID string `json:"report_id"`
	Name     string `json:"name"`
}

func createAccount(ctx context.Context, pool *pgxpool.Pool, id string) (account, error) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO demo.accounts (id, balance_cents) VALUES ($1, 0)
		ON CONFLICT (id) DO NOTHING
	`, id); err != nil {
		return account{}, fmt.Errorf("insert account: %w", err)
	}
	var out account
	if err := pool.QueryRow(ctx, `SELECT id, balance_cents FROM demo.accounts WHERE id = $1`, id).Scan(&out.ID, &out.BalanceCents); err != nil {
		return account{}, fmt.Errorf("load account: %w", err)
	}
	return out, nil
}

func applyTransfer(ctx context.Context, pool *pgxpool.Pool, key, accountID, kind string, amount int64) (movement, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return movement{}, fmt.Errorf("begin transfer: %w", err)
	}
	defer tx.Rollback(ctx)

	var existing movement
	err = tx.QueryRow(ctx, `
		SELECT account_id, abs(delta_cents), kind, delta_cents
		FROM demo.transfers_log WHERE idempotency_key = $1
	`, key).Scan(&existing.AccountID, &existing.AmountCents, &existing.Kind, &existing.DeltaCents)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return movement{}, fmt.Errorf("commit transfer: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return movement{}, fmt.Errorf("load transfer: %w", err)
	}

	delta := amount
	var tagRows int64
	if kind == "debit" {
		delta = -amount
		tag, err := tx.Exec(ctx, `
			UPDATE demo.accounts
			SET balance_cents = balance_cents - $2
			WHERE id = $1 AND balance_cents >= $2
		`, accountID, amount)
		if err != nil {
			return movement{}, fmt.Errorf("debit account: %w", err)
		}
		tagRows = tag.RowsAffected()
	} else {
		tag, err := tx.Exec(ctx, `
			UPDATE demo.accounts
			SET balance_cents = balance_cents + $2
			WHERE id = $1
		`, accountID, amount)
		if err != nil {
			return movement{}, fmt.Errorf("credit account: %w", err)
		}
		tagRows = tag.RowsAffected()
	}
	if tagRows == 0 {
		return movement{}, errNoFunds
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo.transfers_log (idempotency_key, account_id, delta_cents, kind)
		VALUES ($1, $2, $3, $4)
	`, key, accountID, delta, kind); err != nil {
		if isUnique(err) {
			return movement{}, errRetryTransfer
		}
		return movement{}, fmt.Errorf("insert transfer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return movement{}, fmt.Errorf("commit transfer: %w", err)
	}
	return movement{AccountID: accountID, AmountCents: amount, Kind: kind, DeltaCents: delta}, nil
}

func recordReportStep(ctx context.Context, pool *pgxpool.Pool, key string, in reportStep) (reportStep, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return reportStep{}, fmt.Errorf("begin report step: %w", err)
	}
	defer tx.Rollback(ctx)

	var existing reportStep
	err = tx.QueryRow(ctx, `
		SELECT report_id, name FROM demo.report_steps WHERE idempotency_key = $1
	`, key).Scan(&existing.ReportID, &existing.Name)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return reportStep{}, fmt.Errorf("commit report step: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return reportStep{}, fmt.Errorf("load report step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO demo.report_steps (idempotency_key, report_id, name)
		VALUES ($1, $2, $3)
	`, key, in.ReportID, in.Name); err != nil {
		if isUnique(err) {
			return reportStep{}, errRetryReport
		}
		return reportStep{}, fmt.Errorf("insert report step: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return reportStep{}, fmt.Errorf("commit report step: %w", err)
	}
	return in, nil
}

var (
	errNoFunds       = errors.New("not enough money")
	errRetryTransfer = errors.New("transfer conflict")
	errRetryReport   = errors.New("report step conflict")
)
