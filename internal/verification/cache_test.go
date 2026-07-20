package verification

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/stretchr/testify/assert"
)

// countingVerifier records how many times Verify is invoked and returns a canned
// assessment (with the requested email echoed back).
type countingVerifier struct {
	calls      int32
	assessment Assessment
}

func (c *countingVerifier) Verify(_ context.Context, rawEmail string) Assessment {
	atomic.AddInt32(&c.calls, 1)
	a := c.assessment
	a.Email = rawEmail
	return a
}

func (c *countingVerifier) count() int { return int(atomic.LoadInt32(&c.calls)) }

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time      { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
}

func validAssessment() Assessment {
	return Assessment{Status: classify.StatusValid, ReasonCode: classify.ReasonSMTPAccepted, Retryable: false, Confidence: 95}
}

func unknownAssessment() Assessment {
	return Assessment{Status: classify.StatusUnknown, ReasonCode: classify.ReasonSMTPTimeout, Retryable: true, Confidence: 10}
}

func TestCache_HitSkipsWrappedVerifier(t *testing.T) {
	next := &countingVerifier{assessment: validAssessment()}
	clk := newTestClock()
	mem := store.NewMemory[Assessment](100, store.WithClock[Assessment](clk.now))
	cv := NewCachingVerifier(next, mem, 10*time.Minute, time.Minute)

	a1 := cv.Verify(context.Background(), "person@example.com")
	a2 := cv.Verify(context.Background(), "person@example.com")

	assert.Equal(t, 1, next.count(), "second call must be served from cache")
	assert.Equal(t, a1.Status, a2.Status)
	assert.Equal(t, classify.StatusValid, a2.Status)
}

func TestCache_KeyIsCaseInsensitive(t *testing.T) {
	next := &countingVerifier{assessment: validAssessment()}
	mem := store.NewMemory[Assessment](100)
	cv := NewCachingVerifier(next, mem, 10*time.Minute, time.Minute)

	cv.Verify(context.Background(), "Person@Example.com")
	cv.Verify(context.Background(), "  person@example.com  ")

	assert.Equal(t, 1, next.count(), "differently-cased/padded inputs share one entry")
}

func TestCache_UnknownUsesShortTTLAndExpires(t *testing.T) {
	next := &countingVerifier{assessment: unknownAssessment()}
	clk := newTestClock()
	mem := store.NewMemory[Assessment](100, store.WithClock[Assessment](clk.now))
	cv := NewCachingVerifier(next, mem, 10*time.Minute, time.Minute)

	cv.Verify(context.Background(), "user@example.com") // miss -> compute + cache (1m)
	cv.Verify(context.Background(), "user@example.com") // hit
	assert.Equal(t, 1, next.count())

	clk.advance(time.Minute) // unknown TTL elapsed
	cv.Verify(context.Background(), "user@example.com") // miss again -> recompute
	assert.Equal(t, 2, next.count(), "retryable result must be re-verified after the short TTL")
}

func TestCache_ValidSurvivesShortTTLWindow(t *testing.T) {
	next := &countingVerifier{assessment: validAssessment()}
	clk := newTestClock()
	mem := store.NewMemory[Assessment](100, store.WithClock[Assessment](clk.now))
	cv := NewCachingVerifier(next, mem, 10*time.Minute, time.Minute)

	cv.Verify(context.Background(), "person@example.com")
	clk.advance(2 * time.Minute) // past the unknown TTL, well within the valid TTL
	cv.Verify(context.Background(), "person@example.com")

	assert.Equal(t, 1, next.count(), "valid result must remain cached for the full TTL")
}

func TestCache_UnknownTTLDefaultsAndClamps(t *testing.T) {
	next := &countingVerifier{assessment: unknownAssessment()}
	// unknownTTL unset (0) with a large ttl -> defaults to min(ttl, 1m).
	cv := NewCachingVerifier(next, store.NewMemory[Assessment](10), time.Hour, 0)
	assert.Equal(t, time.Minute, cv.unknownTTL)

	// unknownTTL larger than ttl is clamped down to ttl.
	cv2 := NewCachingVerifier(next, store.NewMemory[Assessment](10), 30*time.Second, time.Hour)
	assert.Equal(t, 30*time.Second, cv2.unknownTTL)
}

func TestCache_EmptyEmailNotCached(t *testing.T) {
	next := &countingVerifier{assessment: validAssessment()}
	mem := store.NewMemory[Assessment](100)
	cv := NewCachingVerifier(next, mem, 10*time.Minute, time.Minute)

	cv.Verify(context.Background(), "   ")
	cv.Verify(context.Background(), "   ")
	assert.Equal(t, 2, next.count(), "empty key must not be cached")
	assert.Equal(t, 0, mem.Len())
}
