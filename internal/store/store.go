// Package store provides small storage seams for the verification service. It
// ships an in-memory TTL cache and a durable Postgres backend behind one generic
// interface, so callers (e.g. the result cache) work unchanged with either. It
// imports nothing from the rest of the service, so it never forms an import
// cycle with the packages whose values it stores.
package store

import (
	"context"
	"sync"
	"time"
)

// Store is a generic TTL key/value store. Implementations must be safe for
// concurrent use. Get/Set take a context and may return an error so durable
// backends (Postgres) fit the same interface; the in-memory backend never errors.
type Store[V any] interface {
	// Get returns the live value for key, ok=false if absent or expired.
	Get(ctx context.Context, key string) (value V, ok bool, err error)
	// Set stores value under key for ttl. A ttl <= 0 is a no-op.
	Set(ctx context.Context, key string, value V, ttl time.Duration) error
	// Delete removes key (no error if absent). Used for right-to-erasure.
	Delete(ctx context.Context, key string) error
}

// Purger is implemented by stores that can proactively drop expired entries.
// The in-memory backend implements it (Postgres manages expiry in the database),
// so a background sweeper can free memory between accesses.
type Purger interface {
	// PurgeExpired removes expired entries and returns how many were removed.
	PurgeExpired() int
}

type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Memory is an in-memory Store with lazy expiry and a hard entry cap (FIFO
// eviction). A simple bounded cache, not a true LRU. It ignores the context and
// never returns an error.
type Memory[V any] struct {
	mu         sync.Mutex
	now        func() time.Time
	maxEntries int
	entries    map[string]entry[V]
	order      []string
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
// absent. The context is ignored.
func (m *Memory[V]) Get(_ context.Context, key string) (V, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		var zero V
		return zero, false, nil
	}
	if !m.now().Before(e.expiresAt) { // now >= expiresAt: expired
		m.remove(key)
		var zero V
		return zero, false, nil
	}
	return e.value, true, nil
}

// Set stores value under key for ttl. A non-positive ttl stores nothing.
func (m *Memory[V]) Set(_ context.Context, key string, value V, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[key]; !exists {
		m.evictIfFull()
		m.order = append(m.order, key)
	}
	m.entries[key] = entry[V]{value: value, expiresAt: m.now().Add(ttl)}
	return nil
}

// Delete removes key. The context is ignored; it never errors.
func (m *Memory[V]) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	m.remove(key)
	m.mu.Unlock()
	return nil
}

// PurgeExpired removes every entry whose TTL has elapsed and returns the number
// removed. Safe for concurrent use; intended to be called periodically so
// expired entries don't linger in memory between accesses.
func (m *Memory[V]) PurgeExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	removed := 0
	for key, e := range m.entries {
		if !now.Before(e.expiresAt) { // now >= expiresAt: expired
			delete(m.entries, key)
			removed++
		}
	}
	if removed > 0 {
		// Rebuild the insertion-order slice, dropping purged keys (in place).
		kept := m.order[:0]
		for _, k := range m.order {
			if _, ok := m.entries[k]; ok {
				kept = append(kept, k)
			}
		}
		m.order = kept
	}
	return removed
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
