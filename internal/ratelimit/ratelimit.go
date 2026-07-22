// Package ratelimit provides a small, dependency-free token-bucket limiter keyed
// by an arbitrary client identifier. It is safe for concurrent use.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens   float64
	lastFill time.Time
}

// Limiter is a per-key token-bucket rate limiter. Each key refills at `rate`
// tokens per second up to `burst`.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
}

// New builds a limiter allowing `perMinute` requests per key per minute, with a
// burst equal to perMinute (clamped to >= 1).
func New(perMinute int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(perMinute) / 60.0,
		burst:   float64(perMinute),
		now:     time.Now,
	}
}

// Allow reports whether a request for key may proceed, consuming a token if so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, lastFill: now}
		return true
	}

	// Refill based on elapsed time.
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.lastFill = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Len returns the number of tracked keys (for tests/metrics).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
