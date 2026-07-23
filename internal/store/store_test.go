package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bg = context.Background()

func mustGet[V any](t *testing.T, m *Memory[V], key string) (V, bool) {
	t.Helper()
	v, ok, err := m.Get(bg, key)
	require.NoError(t, err)
	return v, ok
}

func mustSet[V any](t *testing.T, m *Memory[V], key string, v V, ttl time.Duration) {
	t.Helper()
	require.NoError(t, m.Set(bg, key, v, ttl))
}

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
	mustSet(t, m, "k", "v", time.Minute)

	got, ok := mustGet(t, m, "k")
	assert.True(t, ok)
	assert.Equal(t, "v", got)
}

func TestMemory_ExpiresAfterTTL(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	mustSet(t, m, "k", "v", time.Minute)

	clk.add(time.Minute) // now == expiresAt → expired
	_, ok := mustGet(t, m, "k")
	assert.False(t, ok)
	assert.Equal(t, 0, m.Len(), "expired entry should be purged on Get")
}

func TestMemory_MissingKey(t *testing.T) {
	m := NewMemory[int](10)
	_, ok := mustGet(t, m, "absent")
	assert.False(t, ok)
}

func TestMemory_NonPositiveTTLIsNoop(t *testing.T) {
	m := NewMemory[string](10)
	mustSet(t, m, "k", "v", 0)
	mustSet(t, m, "k2", "v", -time.Second)
	assert.Equal(t, 0, m.Len())
	_, ok := mustGet(t, m, "k")
	assert.False(t, ok)
}

func TestMemory_EvictsOldestWhenFull(t *testing.T) {
	m := NewMemory[string](2)
	mustSet(t, m, "a", "1", time.Minute)
	mustSet(t, m, "b", "2", time.Minute)
	mustSet(t, m, "c", "3", time.Minute) // evicts "a"

	assert.Equal(t, 2, m.Len())
	_, ok := mustGet(t, m, "a")
	assert.False(t, ok, "oldest entry a should have been evicted")
	_, ok = mustGet(t, m, "b")
	assert.True(t, ok)
	_, ok = mustGet(t, m, "c")
	assert.True(t, ok)
}

func TestMemory_Delete(t *testing.T) {
	m := NewMemory[string](10)
	mustSet(t, m, "k", "v", time.Minute)
	require.NoError(t, m.Delete(bg, "k"))
	_, ok := mustGet(t, m, "k")
	assert.False(t, ok)
	require.NoError(t, m.Delete(bg, "absent"), "deleting an absent key is a no-op")
}

func TestMemory_ReSetKeepsSingleEntry(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	mustSet(t, m, "k", "v1", time.Minute)
	mustSet(t, m, "k", "v2", time.Minute)

	assert.Equal(t, 1, m.Len())
	got, ok := mustGet(t, m, "k")
	assert.True(t, ok)
	assert.Equal(t, "v2", got)
}

func TestMemory_PurgeExpired(t *testing.T) {
	clk := newClock()
	m := NewMemory[string](10, WithClock[string](clk.now))
	mustSet(t, m, "short", "a", time.Minute)
	mustSet(t, m, "long", "b", time.Hour)
	require.Equal(t, 2, m.Len())

	clk.add(2 * time.Minute) // "short" expired, "long" still live

	removed := m.PurgeExpired()
	assert.Equal(t, 1, removed)
	assert.Equal(t, 1, m.Len())

	// The live entry is still retrievable; the expired one is gone.
	v, ok := mustGet(t, m, "long")
	assert.True(t, ok)
	assert.Equal(t, "b", v)
	_, ok = mustGet(t, m, "short")
	assert.False(t, ok)

	// Insertion order is consistent: adding past the cap evicts "long" (the only
	// remaining key), not a stale purged key.
	m2 := NewMemory[string](2, WithClock[string](clk.now))
	mustSet(t, m2, "x", "1", time.Minute)
	mustSet(t, m2, "y", "2", time.Hour)
	clk.add(2 * time.Minute) // "x" expired
	m2.PurgeExpired()
	mustSet(t, m2, "z", "3", time.Hour)
	mustSet(t, m2, "w", "4", time.Hour) // cap=2 -> evicts oldest live key "y"
	_, ok = mustGet(t, m2, "y")
	assert.False(t, ok)
	_, okZ := mustGet(t, m2, "z")
	_, okW := mustGet(t, m2, "w")
	assert.True(t, okZ)
	assert.True(t, okW)
}

// Memory satisfies the Purger interface.
var _ Purger = (*Memory[string])(nil)
