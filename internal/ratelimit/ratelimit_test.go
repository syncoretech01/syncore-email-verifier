package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLimiter_AllowsBurstThenBlocks(t *testing.T) {
	l := New(60) // 60/min => burst 60, 1 token/sec
	clk := &clock{t: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)}
	l.now = clk.now

	allowed := 0
	for i := 0; i < 60; i++ {
		if l.Allow("k") {
			allowed++
		}
	}
	assert.Equal(t, 60, allowed, "the full burst is allowed")
	assert.False(t, l.Allow("k"), "the 61st request is blocked")
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	l := New(60) // 1 token/sec
	clk := &clock{t: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)}
	l.now = clk.now

	for i := 0; i < 60; i++ {
		l.Allow("k")
	}
	assert.False(t, l.Allow("k"))

	clk.advance(2 * time.Second) // ~2 tokens refilled
	assert.True(t, l.Allow("k"))
	assert.True(t, l.Allow("k"))
	assert.False(t, l.Allow("k"))
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := New(1) // 1/min, burst 1
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))
	assert.True(t, l.Allow("b"), "a different key has its own bucket")
}
