// Package feedback ingests real sending outcomes (bounces, complaints,
// deliveries, engagement) and accumulates per-domain reputation priors. These
// priors are the closed-loop accuracy signal: what actually happened when mail
// was sent, fed back to sharpen future scoring. Storage is in-memory and
// concurrency-safe.
package feedback

import (
	"strings"
	"sync"
)

// EventType is a normalized sending outcome.
type EventType string

const (
	EventDelivered  EventType = "delivered"
	EventBounced    EventType = "bounced" // hard bounce
	EventComplained EventType = "complained"
	EventEngaged    EventType = "engaged" // open/click/reply
)

// Event is a single normalized outcome for an address.
type Event struct {
	Email string    `json:"email"`
	Type  EventType `json:"type"`
}

// DomainReputation is the accumulated per-domain outcome tally.
type DomainReputation struct {
	Delivered  int `json:"delivered"`
	Bounced    int `json:"bounced"`
	Complained int `json:"complained"`
	Engaged    int `json:"engaged"`
}

// BounceRate is bounced / (delivered + bounced); 0 when there is no send history.
func (r DomainReputation) BounceRate() float64 {
	total := r.Delivered + r.Bounced
	if total == 0 {
		return 0
	}
	return float64(r.Bounced) / float64(total)
}

// Store accumulates per-domain reputation from ingested events.
type Store struct {
	mu      sync.RWMutex
	domains map[string]*DomainReputation
}

// New builds an empty store.
func New() *Store {
	return &Store{domains: make(map[string]*DomainReputation)}
}

// Record folds one event into the per-domain tally. Unknown event types and
// addresses without a domain are ignored.
func (s *Store) Record(e Event) {
	domain := domainOf(e.Email)
	if domain == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.domains[domain]
	if !ok {
		r = &DomainReputation{}
		s.domains[domain] = r
	}
	switch e.Type {
	case EventDelivered:
		r.Delivered++
	case EventBounced:
		r.Bounced++
	case EventComplained:
		r.Complained++
	case EventEngaged:
		r.Engaged++
	}
}

// Domain returns the reputation for a domain, ok=false if none recorded.
func (s *Store) Domain(domain string) (DomainReputation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.domains[strings.ToLower(strings.TrimSpace(domain))]
	if !ok {
		return DomainReputation{}, false
	}
	return *r, true
}

func domainOf(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if i := strings.LastIndexByte(email, '@'); i >= 0 && i < len(email)-1 {
		return email[i+1:]
	}
	return ""
}
