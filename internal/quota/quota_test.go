package quota

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type clk struct{ t time.Time }

func (c *clk) now() time.Time { return c.t }

func TestQuota_BlocksOverDailyLimit(t *testing.T) {
	q := New(3)
	for i := 0; i < 3; i++ {
		assert.True(t, q.Allow("k"))
	}
	assert.False(t, q.Allow("k"), "4th request over a daily limit of 3 is blocked")
	assert.Equal(t, 0, q.Remaining("k"))
}

func TestQuota_ResetsAtDayBoundary(t *testing.T) {
	c := &clk{t: time.Date(2026, 7, 21, 23, 59, 0, 0, time.UTC)}
	q := New(2)
	q.now = c.now

	assert.True(t, q.Allow("k"))
	assert.True(t, q.Allow("k"))
	assert.False(t, q.Allow("k"))

	c.t = c.t.Add(2 * time.Minute) // crosses into the next UTC day
	assert.True(t, q.Allow("k"), "the daily counter resets at the UTC day boundary")
}

func TestQuota_KeysAreIndependent(t *testing.T) {
	q := New(1)
	assert.True(t, q.Allow("a"))
	assert.False(t, q.Allow("a"))
	assert.True(t, q.Allow("b"))
}
