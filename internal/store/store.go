// Package store provides small, dependency-free storage seams for the
// verification service. Phase 2 ships an in-memory TTL cache; a durable backend
// (e.g. Postgres) can implement the same Store interface later without changing
// callers. It imports nothing from the rest of the service, so it never forms an
// import cycle with the packages whose values it stores.
package store

import (
	"sync"
	"time"
)

// Store is a generic TTL key/value store. Implementations must be safe for
// concurrent use by multiple goroutines.
type Store[V any] interface {
	// Get returns the live value for key, or ok=false if absent or expired.
	Get(key string) (value V, ok bool)
	// Set stores value under key for ttl. A ttl <= 0 is a no-op (nothing stored).
	Set(key string, value V, ttl time.Duration)
	// Len reports the number of stored entries (may include not-yet-purged
	// expired ones). Intended for tests and metrics.
	Len() int
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Memory is an in-memory Store with lazy expiry and a hard entry cap. When the
// cap is reached, the oldest-inserted entries are evicted first (FIFO). It is a
// simple bounded cache, not a true LRU — adequate for the service's short-TTL
// result cache.
type Memory[V any] struct {
	mu         sync.Mutex
	now        func() time.Time
	maxEntries int
	entries    map[string]entry[V]
	order      []string // insertion order, for FIFO eviction
}

// Option configures a Memory store.
type Option[V any] func(*Memory[V])

// WithClock injects a time source, enabling deterministic TTL tests.
func WithClock[V any](now func() time.Time) Option[V] {
	return func(m *Memory[V]) {
		if now != nil {
			m.now = now
		}
	}
}

// NewMemory builds an in-memory store holding at most maxEntries entries
// (clamped to >= 1).
func NewMemory[V any](maxEntries int, opts ...Option[V]) *Memory[V] {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	m := &Memory[V]{
		now:        time.Now,
		maxEntries: maxEntries,
		entries:    make(map[string]entry[V], maxEntries),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Get returns the live value for key. An expired entry is purged and reported as
// absent.
func (m *Memory[V]) Get(key string) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !m.now().Before(e.expiresAt) { // now >= expiresAt: expired
		m.remove(key)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Set stores value under key for ttl. A non-positive ttl stores nothing.
func (m *Memory[V]) Set(key string, value V, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[key]; !exists {
		m.evictIfFull()
		m.order = append(m.order, key)
	}
	m.entries[key] = entry[V]{value: value, expiresAt: m.now().Add(ttl)}
}

// Len reports the current number of stored entries.
func (m *Memory[V]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// evictIfFull drops oldest-inserted entries until there is room for one more.
// The caller holds the lock.
func (m *Memory[V]) evictIfFull() {
	for len(m.entries) >= m.maxEntries && len(m.order) > 0 {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.entries, oldest)
	}
}

// remove deletes key from both the map and the insertion-order slice. The caller
// holds the lock.
func (m *Memory[V]) remove(key string) {
	delete(m.entries, key)
	for i, k := range m.order {
		if k == key {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}
