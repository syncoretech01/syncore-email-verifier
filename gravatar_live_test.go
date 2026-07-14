//go:build live

// Live Gravatar tests reaching www.gravatar.com. Excluded from the default
// deterministic suite; run with: go test -tags=live ./...
package emailverifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckGravatarOK(t *testing.T) {
	email := "alex@pagerduty.com"

	gravatar, err := verifier.CheckGravatar(email)
	assert.NoError(t, err)
	assert.True(t, gravatar.HasGravatar)
	assert.NotEmpty(t, gravatar.GravatarUrl)
}

func TestCheckGravatarFailed(t *testing.T) {
	email := "MyemailaddressHasNoGravatar@example.com"

	gravatar, err := verifier.CheckGravatar(email)
	assert.NoError(t, err)
	assert.False(t, gravatar.HasGravatar)
	assert.Empty(t, gravatar.GravatarUrl)
}
