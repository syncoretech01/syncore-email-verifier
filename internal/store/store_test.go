package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeClock is a manually advanced time source.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
}

func TestMemory_SetGetWithinTTL(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	m.Set("k", "v", time.Minute)

	got, ok := m.Get("k")
	assert.True(t, ok)
	assert.Equal(t, "v", got)
}

func TestMemory_ExpiresAfterTTL(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	m.Set("k", "v", time.Minute)

	clk.add(time.Minute) // now == expiresAt → expired
	_, ok := m.Get("k")
	assert.False(t, ok)
	assert.Equal(t, 0, m.Len(), "expired entry should be purged on Get")
}

func TestMemory_MissingKey(t *testing.T) {
	m := NewMemory[int](10)
	_, ok := m.Get("absent")
	assert.False(t, ok)
}

func TestMemory_NonPositiveTTLIsNoop(t *testing.T) {
	m := NewMemory[string](10)
	m.Set("k", "v", 0)
	m.Set("k2", "v", -time.Second)
	assert.Equal(t, 0, m.Len())
	_, ok := m.Get("k")
	assert.False(t, ok)
}

func TestMemory_EvictsOldestWhenFull(t *testing.T) {
	m := NewMemory[string](2)
	m.Set("a", "1", time.Minute)
	m.Set("b", "2", time.Minute)
	m.Set("c", "3", time.Minute) // evicts "a"

	assert.Equal(t, 2, m.Len())
	_, ok := m.Get("a")
	assert.False(t, ok, "oldest entry a should have been evicted")
	_, ok = m.Get("b")
	assert.True(t, ok)
	_, ok = m.Get("c")
	assert.True(t, ok)
}

func TestMemory_ReSetKeepsSingleEntry(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	m.Set("k", "v1", time.Minute)
	m.Set("k", "v2", time.Minute)

	assert.Equal(t, 1, m.Len())
	got, ok := m.Get("k")
	assert.True(t, ok)
	assert.Equal(t, "v2", got)
}
