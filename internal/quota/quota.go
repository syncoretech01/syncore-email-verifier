// Package quota enforces a per-key daily request cap. It is in-memory (counts
// reset on process restart and at the UTC day boundary) and concurrency-safe.
// It complements the rate limiter: rate limits smooth bursts, the quota bounds
// total daily volume per client.
package quota

import (
	"sync"
	"time"
)

// Quota tracks per-key usage within the current UTC day.
type Quota struct {
	mu     sync.Mutex
	limit  int
	now    func() time.Time
	day    string
	counts map[string]int
}

// New builds a quota allowing dailyLimit requests per key per UTC day (clamped
// to >= 1).
func New(dailyLimit int) *Quota {
	if dailyLimit < 1 {
		dailyLimit = 1
	}
	return &Quota{limit: dailyLimit, now: time.Now, counts: make(map[string]int)}
}

// Allow consumes one unit for key, returning false once the daily limit is hit.
func (q *Quota) Allow(key string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	day := q.now().UTC().Format("2006-01-02")
	if day != q.day {
		q.day = day
		q.counts = make(map[string]int)
	}
	if q.counts[key] >= q.limit {
		return false
	}
	q.counts[key]++
	return true
}

// Remaining reports how many units key has left today.
func (q *Quota) Remaining(key string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.now().UTC().Format("2006-01-02") != q.day {
		return q.limit
	}
	r := q.limit - q.counts[key]
	if r < 0 {
		r = 0
	}
	return r
}
