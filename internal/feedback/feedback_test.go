package feedback

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplay_UpdatesPriors feeds a synthetic stream of outcomes and asserts the
// resulting per-domain reputation — the roadmap's deterministic replay check.
func TestReplay_UpdatesPriors(t *testing.T) {
	s := New()
	events := []Event{
		{"a@good.com", EventDelivered},
		{"b@good.com", EventDelivered},
		{"c@good.com", EventEngaged},
		{"a@bad.com", EventBounced},
		{"b@bad.com", EventBounced},
		{"c@bad.com", EventDelivered},
		{"d@bad.com", EventComplained},
	}
	for _, e := range events {
		s.Record(e)
	}

	good, ok := s.Domain("good.com")
	require.True(t, ok)
	assert.Equal(t, 2, good.Delivered)
	assert.Equal(t, 1, good.Engaged)
	assert.Equal(t, 0.0, good.BounceRate())

	bad, ok := s.Domain("bad.com")
	require.True(t, ok)
	assert.Equal(t, 2, bad.Bounced)
	assert.Equal(t, 1, bad.Delivered)
	assert.Equal(t, 1, bad.Complained)
	assert.InDelta(t, 2.0/3.0, bad.BounceRate(), 1e-9)
}

func TestRecord_NormalizesDomainAndIgnoresBad(t *testing.T) {
	s := New()
	s.Record(Event{Email: "  User@Example.COM ", Type: EventBounced})
	s.Record(Event{Email: "no-at-sign", Type: EventBounced}) // ignored
	s.Record(Event{Email: "x@example.com", Type: "weird"})   // unknown type -> no counter

	rep, ok := s.Domain("EXAMPLE.com")
	require.True(t, ok)
	assert.Equal(t, 1, rep.Bounced)

	_, ok = s.Domain("missing.com")
	assert.False(t, ok)
}
