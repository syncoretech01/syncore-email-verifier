// Package suppression maintains a do-not-verify set of addresses. Suppressed
// addresses are short-circuited before any network check.
package suppression

import (
	"strings"
	"sync"
)

// Set is a concurrency-safe set of normalized email addresses.
type Set struct {
	mu      sync.RWMutex
	entries map[string]struct{}
}

// New builds an empty set.
func New() *Set {
	return &Set{entries: make(map[string]struct{})}
}

// NewFromList builds a set from raw addresses.
func NewFromList(addrs []string) *Set {
	s := New()
	for _, a := range addrs {
		s.Add(a)
	}
	return s
}

// normalize lowercases and trims so lookups are case/space-insensitive.
func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Add inserts an address (no-op for empty input).
func (s *Set) Add(email string) {
	n := normalize(email)
	if n == "" {
		return
	}
	s.mu.Lock()
	s.entries[n] = struct{}{}
	s.mu.Unlock()
}

// Remove deletes an address.
func (s *Set) Remove(email string) {
	s.mu.Lock()
	delete(s.entries, normalize(email))
	s.mu.Unlock()
}

// Contains reports whether an address is suppressed.
func (s *Set) Contains(email string) bool {
	s.mu.RLock()
	_, ok := s.entries[normalize(email)]
	s.mu.RUnlock()
	return ok
}

// Len returns the number of suppressed addresses.
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
