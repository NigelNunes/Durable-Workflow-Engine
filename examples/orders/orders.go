// Package orders is the order-fulfilment workflow. It imports the SDK only.
package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nigelnunes/durable-flow/sdk/activity"
	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

// Input is one order to fulfil. PaceSeconds is a durable pause between steps
// so a live demo is easy to narrate (0 skips the pause).
type Input struct {
	OrderID     string `json:"order_id"`
	AmountCents int    `json:"amount_cents"`
	SKU         string `json:"sku"`
	Qty         int    `json:"qty"`
	Email       string `json:"email"`
	PaceSeconds int    `json:"pace_seconds"`
}

// Result is the workflow return value.
type Result struct {
	Status string `json:"status"`
}

// ChargeIn is the ChargeCard activity input.
type ChargeIn struct {
	OrderID     string `json:"order_id"`
	AmountCents int    `json:"amount_cents"`
}

// ChargeOut is the stored charge.
type ChargeOut struct {
	ChargeID int64 `json:"charge_id"`
}

// ReserveIn is the ReserveStock activity input.
type ReserveIn struct {
	OrderID string `json:"order_id"`
	SKU     string `json:"sku"`
	Qty     int    `json:"qty"`
}

// ReserveOut is the stored reservation.
type ReserveOut struct {
	OrderID string `json:"order_id"`
	SKU     string `json:"sku"`
	Qty     int    `json:"qty"`
}

// ReceiptIn is the SendReceipt activity input.
type ReceiptIn struct {
	OrderID  string `json:"order_id"`
	ChargeID int64  `json:"charge_id"`
	Email    string `json:"email"`
}

// Workflow charges the card, reserves stock, then sends a receipt.
func Workflow(ctx workflow.Context, in Input) (Result, error) {
	retry := workflow.ActivityOptions{Retry: workflow.RetryPolicy{
		MaxAttempts:        5,
		InitialInterval:    500 * time.Millisecond,
		BackoffCoefficient: 2,
		MaxInterval:        10 * time.Second,
	}}

	charge, err := workflow.Activity[ChargeIn, ChargeOut](ctx, "ChargeCard",
		ChargeIn{OrderID: in.OrderID, AmountCents: in.AmountCents}, retry)
	if err != nil {
		return Result{}, err
	}
	if err := pace(ctx, in.PaceSeconds); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[ReserveIn, ReserveOut](ctx, "ReserveStock",
		ReserveIn{OrderID: in.OrderID, SKU: in.SKU, Qty: in.Qty}, retry); err != nil {
		return Result{}, err
	}
	if err := pace(ctx, in.PaceSeconds); err != nil {
		return Result{}, err
	}
	if _, err := workflow.Activity[ReceiptIn, struct{}](ctx, "SendReceipt",
		ReceiptIn{OrderID: in.OrderID, ChargeID: charge.ChargeID, Email: in.Email}, retry); err != nil {
		return Result{}, err
	}
	return Result{Status: "fulfilled"}, nil
}

func pace(ctx workflow.Context, seconds int) error {
	if seconds <= 0 {
		return nil
	}
	return ctx.Sleep(time.Duration(seconds) * time.Second)
}

// ChargeCard calls the payments service with this activity's idempotency key.
func ChargeCard(ctx context.Context, in ChargeIn) (ChargeOut, error) {
	var out ChargeOut
	if err := post(ctx, "/payments/charge", in, &out); err != nil {
		return ChargeOut{}, err
	}
	return out, nil
}

// ReserveStock calls the inventory service with this activity's idempotency key.
func ReserveStock(ctx context.Context, in ReserveIn) (ReserveOut, error) {
	var out ReserveOut
	if err := post(ctx, "/inventory/reserve", in, &out); err != nil {
		return ReserveOut{}, err
	}
	return out, nil
}

// SendReceipt calls the email service with this activity's idempotency key.
func SendReceipt(ctx context.Context, in ReceiptIn) (struct{}, error) {
	body := struct {
		OrderID  string `json:"order_id"`
		To       string `json:"to"`
		Template string `json:"template"`
	}{OrderID: in.OrderID, To: in.Email, Template: "receipt"}
	if err := post(ctx, "/email/send", body, nil); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

func post(ctx context.Context, path string, body any, dest any) error {
	base := strings.TrimRight(os.Getenv("SERVICES_URL"), "/")
	if base == "" {
		return fmt.Errorf("SERVICES_URL is required")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", activity.GetInfo(ctx).IdempotencyKey)
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer res.Body.Close()
	resp, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if res.StatusCode == http.StatusConflict {
		return workflow.NonRetryable(fmt.Errorf("%s: %s", path, resp))
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: http %d: %s", path, res.StatusCode, resp)
	}
	if dest != nil && len(resp) > 0 {
		if err := json.Unmarshal(resp, dest); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}
