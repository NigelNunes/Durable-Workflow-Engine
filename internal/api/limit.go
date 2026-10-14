package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	startCapacity = 12
	startRefill   = 2.0 // tokens per second
)

// startLimiter is a per-IP token bucket. Twelve tokens, refilled at two per second.
type startLimiter struct {
	now  func() time.Time
	mu   sync.Mutex
	byIP map[string]*ipBucket
}

type ipBucket struct {
	tokens float64
	at     time.Time
}

func newStartLimiter(now func() time.Time) *startLimiter {
	return &startLimiter{now: now, byIP: map[string]*ipBucket{}}
}

func (l *startLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.byIP[ip]
	if b == nil {
		b = &ipBucket{tokens: startCapacity, at: now}
		l.byIP[ip] = b
	}
	elapsed := now.Sub(b.at).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * startRefill
		if b.tokens > startCapacity {
			b.tokens = startCapacity
		}
		b.at = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
