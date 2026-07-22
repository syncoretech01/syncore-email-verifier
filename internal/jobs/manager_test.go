package jobs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubVerifier returns assessments from a function, tracking attempts per email.
type stubVerifier struct {
	mu    sync.Mutex
	calls map[string]int
	fn    func(email string, attempt int) verification.Assessment
}

func newStub(fn func(email string, attempt int) verification.Assessment) *stubVerifier {
	return &stubVerifier{calls: map[string]int{}, fn: fn}
}

func (s *stubVerifier) Verify(_ context.Context, email string) verification.Assessment {
	s.mu.Lock()
	s.calls[email]++
	n := s.calls[email]
	s.mu.Unlock()
	return s.fn(email, n)
}

func valid(email string) verification.Assessment {
	return verification.Assessment{Email: email, Status: classify.StatusValid, ReasonCode: classify.ReasonSMTPAccepted, Retryable: false}
}

func unknown(email string) verification.Assessment {
	return verification.Assessment{Email: email, Status: classify.StatusUnknown, ReasonCode: classify.ReasonSMTPTimeout, Retryable: true}
}

func waitDone(t *testing.T, m *Manager, id string) *Batch {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, ok := m.Get(id)
		require.True(t, ok)
		if b.State == StateDone {
			return b
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("batch %s did not finish in time", id)
	return nil
}

func TestManager_ProcessesBatchWithCounts(t *testing.T) {
	stub := newStub(func(email string, _ int) verification.Assessment {
		if email == "bad@x.com" {
			return verification.Assessment{Email: email, Status: classify.StatusInvalid, ReasonCode: classify.ReasonSyntaxInvalid}
		}
		return valid(email)
	})
	m := NewManager(stub, Config{Workers: 2, ItemConcurrency: 4})
	m.Start()
	defer m.Stop()

	b, err := m.Submit([]string{"a@x.com", "bad@x.com", "c@x.com"}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, StateQueued, b.State)

	done := waitDone(t, m, b.ID)
	assert.Equal(t, StateDone, done.State)
	assert.Equal(t, 3, done.Total)
	assert.Equal(t, 3, done.Done)
	assert.Equal(t, 2, done.Counts["valid"])
	assert.Equal(t, 1, done.Counts["invalid"])

	items, total, ok := m.Results(b.ID, 0, 100)
	require.True(t, ok)
	assert.Equal(t, 3, total)
	assert.Equal(t, "a@x.com", items[0].Email, "results preserve input order")
	assert.Equal(t, "c@x.com", items[2].Email)
}

func TestManager_RetriesRetryableUntilResolved(t *testing.T) {
	// unknown on attempts 1-2, valid on attempt 3.
	stub := newStub(func(email string, attempt int) verification.Assessment {
		if attempt >= 3 {
			return valid(email)
		}
		return unknown(email)
	})
	m := NewManager(stub, Config{Workers: 1, RetryMax: 3}) // backoff 0 => fast
	m.Start()
	defer m.Stop()

	b, _ := m.Submit([]string{"u@x.com"}, "", nil)
	done := waitDone(t, m, b.ID)
	assert.Equal(t, 1, done.Counts["valid"])

	items, _, _ := m.Results(b.ID, 0, 10)
	assert.Equal(t, "valid", string(items[0].Assessment.Status))
	assert.Equal(t, 3, items[0].Attempts)
}

func TestManager_RetryStopsAtCap(t *testing.T) {
	stub := newStub(func(email string, _ int) verification.Assessment { return unknown(email) }) // never resolves
	m := NewManager(stub, Config{Workers: 1, RetryMax: 2})
	m.Start()
	defer m.Stop()

	b, _ := m.Submit([]string{"u@x.com"}, "", nil)
	done := waitDone(t, m, b.ID)
	assert.Equal(t, 1, done.Counts["unknown"])

	items, _, _ := m.Results(b.ID, 0, 10)
	assert.Equal(t, 3, items[0].Attempts, "1 initial + RetryMax(2) retries")
}

func TestManager_ResultsPagination(t *testing.T) {
	stub := newStub(func(email string, _ int) verification.Assessment { return valid(email) })
	m := NewManager(stub, Config{Workers: 2})
	m.Start()
	defer m.Stop()

	emails := make([]string, 25)
	for i := range emails {
		emails[i] = fmt.Sprintf("u%02d@x.com", i)
	}
	b, _ := m.Submit(emails, "", nil)
	waitDone(t, m, b.ID)

	page, total, ok := m.Results(b.ID, 10, 5)
	require.True(t, ok)
	assert.Equal(t, 25, total)
	require.Len(t, page, 5)
	assert.Equal(t, "u10@x.com", page[0].Email)
	assert.Equal(t, "u14@x.com", page[4].Email)
}

func TestManager_GetUnknownBatch(t *testing.T) {
	m := NewManager(newStub(func(e string, _ int) verification.Assessment { return valid(e) }), Config{})
	m.Start()
	defer m.Stop()
	_, ok := m.Get("does-not-exist")
	assert.False(t, ok)
}

func TestManager_StopDrainsAndRejectsNewWork(t *testing.T) {
	stub := newStub(func(email string, _ int) verification.Assessment { return valid(email) })
	m := NewManager(stub, Config{Workers: 2})
	m.Start()

	b, err := m.Submit([]string{"a@x.com", "b@x.com"}, "", nil)
	require.NoError(t, err)

	m.Stop() // drains the queued batch before returning

	done, ok := m.Get(b.ID)
	require.True(t, ok)
	assert.Equal(t, StateDone, done.State, "queued work must finish during drain")

	_, err = m.Submit([]string{"late@x.com"}, "", nil)
	assert.ErrorIs(t, err, ErrStopped)
}
