package suppression

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSet_ContainsNormalized(t *testing.T) {
	s := NewFromList([]string{"Blocked@Example.com", "  spammer@x.com  "})
	assert.True(t, s.Contains("blocked@example.com"))
	assert.True(t, s.Contains("BLOCKED@EXAMPLE.COM"))
	assert.True(t, s.Contains("spammer@x.com"))
	assert.False(t, s.Contains("someone@example.com"))
	assert.Equal(t, 2, s.Len())
}

func TestSet_AddRemove(t *testing.T) {
	s := New()
	s.Add("a@x.com")
	assert.True(t, s.Contains("a@x.com"))
	s.Remove("A@X.com")
	assert.False(t, s.Contains("a@x.com"))
}

func TestSet_IgnoresEmpty(t *testing.T) {
	s := New()
	s.Add("   ")
	assert.Equal(t, 0, s.Len())
}
