// Package jobs runs asynchronous batch verifications through a bounded worker
// pool, retries retryable results with backoff, and optionally posts an
// HMAC-signed webhook on completion. Storage is in-memory: batches live for the
// process lifetime (a durable Postgres-backed queue can replace the store later
// without changing the HTTP surface).
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/AfterShip/email-verifier/internal/verification"
)

// ErrStopped is returned by Submit after the manager has been stopped.
var ErrStopped = errors.New("jobs: manager stopped")

// State is a batch lifecycle state.
type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateDone    State = "done"
)

// Verifier is the verification behavior the manager needs. *verification.Service
// and the caching decorator both satisfy it.
type Verifier interface {
	Verify(ctx context.Context, email string) verification.Assessment
}

// ItemResult is one email's outcome within a batch.
type ItemResult struct {
	Email      string                  `json:"email"`
	Assessment verification.Assessment `json:"assessment"`
	Attempts   int                     `json:"attempts"`
}

// Batch is an async verification job and its progress.
type Batch struct {
	ID          string          `json:"batch_id"`
	State       State           `json:"state"`
	Total       int             `json:"total"`
	Done        int             `json:"done"`
	Counts      map[string]int  `json:"counts"`
	CallbackURL string          `json:"callback_url,omitempty"`
	Meta        json.RawMessage `json:"meta,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	inputs  []string
	results []ItemResult
}

// Config configures a Manager.
type Config struct {
	Workers         int           // batch-level worker goroutines
	ItemConcurrency int           // per-batch email concurrency
	RetryMax        int           // max retries for a retryable item
	RetryBackoff    time.Duration // base backoff between retries (multiplied by attempt)
	Webhook         *Webhook      // nil disables completion webhooks
	Clock           func() time.Time
}

// Manager owns the in-memory batch store and worker pool.
type Manager struct {
	verifier     Verifier
	workers      int
	itemConc     int
	retryMax     int
	retryBackoff time.Duration
	webhook      *Webhook
	clock        func() time.Time

	mu      sync.RWMutex
	batches map[string]*Batch
	queue   chan string
	wg      sync.WaitGroup
	stopped bool
}

// NewManager builds a Manager. Call Start before submitting.
func NewManager(v Verifier, cfg Config) *Manager {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.ItemConcurrency <= 0 {
		cfg.ItemConcurrency = 10
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Manager{
		verifier:     v,
		workers:      cfg.Workers,
		itemConc:     cfg.ItemConcurrency,
		retryMax:     cfg.RetryMax,
		retryBackoff: cfg.RetryBackoff,
		webhook:      cfg.Webhook,
		clock:        cfg.Clock,
		batches:      make(map[string]*Batch),
		queue:        make(chan string, 1024),
	}
}

// Start launches the worker pool.
func (m *Manager) Start() {
	for i := 0; i < m.workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
}

// Stop drains: no new work is accepted, queued batches finish, then it returns.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		close(m.queue)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Submit enqueues a batch and returns it in the queued state.
func (m *Manager) Submit(emails []string, callbackURL string, meta json.RawMessage) (*Batch, error) {
	now := m.clock()
	b := &Batch{
		ID:          newID(),
		State:       StateQueued,
		Total:       len(emails),
		Counts:      map[string]int{},
		CallbackURL: callbackURL,
		Meta:        meta,
		CreatedAt:   now,
		UpdatedAt:   now,
		inputs:      emails,
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, ErrStopped
	}
	m.batches[b.ID] = b
	m.mu.Unlock()

	m.queue <- b.ID
	return m.snapshot(b.ID), nil
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for id := range m.queue {
		m.process(context.Background(), id)
	}
}

func (m *Manager) process(ctx context.Context, id string) {
	m.mu.Lock()
	b := m.batches[id]
	b.State = StateRunning
	b.UpdatedAt = m.clock()
	inputs := b.inputs
	m.mu.Unlock()

	results := make([]ItemResult, len(inputs))
	sem := make(chan struct{}, m.itemConc)
	var wg sync.WaitGroup
	for i, email := range inputs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, email string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = m.verifyWithRetry(ctx, email)
		}(i, email)
	}
	wg.Wait()

	counts := map[string]int{}
	for _, r := range results {
		counts[string(r.Assessment.Status)]++
	}

	m.mu.Lock()
	b.results = results
	b.Counts = counts
	b.Done = len(results)
	b.State = StateDone
	b.UpdatedAt = m.clock()
	callback := b.CallbackURL
	summary := b.webhookSummary()
	m.mu.Unlock()

	if m.webhook != nil && callback != "" {
		_ = m.webhook.Send(ctx, callback, summary)
	}
}

// verifyWithRetry runs one email, retrying while it is retryable up to RetryMax.
// It never upgrades a result without a real re-verification.
func (m *Manager) verifyWithRetry(ctx context.Context, email string) ItemResult {
	a := m.verifier.Verify(ctx, email)
	attempts := 1
	for a.Retryable && attempts <= m.retryMax {
		if m.retryBackoff > 0 {
			select {
			case <-time.After(m.retryBackoff * time.Duration(attempts)):
			case <-ctx.Done():
				return ItemResult{Email: email, Assessment: a, Attempts: attempts}
			}
		}
		a = m.verifier.Verify(ctx, email)
		attempts++
	}
	return ItemResult{Email: email, Assessment: a, Attempts: attempts}
}

// Get returns a snapshot of a batch's progress (without the full results slice).
func (m *Manager) Get(id string) (*Batch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.batches[id]; !ok {
		return nil, false
	}
	return m.snapshotLocked(id), true
}

// Results returns a page of a batch's results plus the total count.
func (m *Manager) Results(id string, offset, limit int) (items []ItemResult, total int, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, exists := m.batches[id]
	if !exists {
		return nil, 0, false
	}
	total = len(b.results)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	out := make([]ItemResult, end-offset)
	copy(out, b.results[offset:end])
	return out, total, true
}

func (m *Manager) snapshot(id string) *Batch {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotLocked(id)
}

// snapshotLocked returns a copy safe to hand out. The caller holds the lock.
func (m *Manager) snapshotLocked(id string) *Batch {
	b := m.batches[id]
	cp := *b
	cp.inputs = nil
	cp.results = nil
	return &cp
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
