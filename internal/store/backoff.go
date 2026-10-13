package store

import (
	"math"
	"math/rand"
	"time"
)

type retryPolicy struct {
	MaxAttempts       int     `json:"max_attempts"`
	InitialIntervalMS int64   `json:"initial_interval_ms"`
	Backoff           float64 `json:"backoff"`
	MaxIntervalMS     int64   `json:"max_interval_ms"`
}

// backoffDelay is InitialInterval * Backoff^(attempt-1), capped at MaxInterval,
// plus up to 20 percent jitter. The result is never shorter than the formula.
func backoffDelay(attempt int, policy retryPolicy) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	coeff := policy.Backoff
	if coeff <= 0 {
		coeff = 1
	}
	delay := float64(policy.InitialIntervalMS) * math.Pow(coeff, float64(attempt-1))
	if policy.MaxIntervalMS > 0 && delay > float64(policy.MaxIntervalMS) {
		delay = float64(policy.MaxIntervalMS)
	}
	if delay < 0 {
		delay = 0
	}
	jitter := delay * rand.Float64() * 0.2
	return time.Duration(delay+jitter) * time.Millisecond
}
