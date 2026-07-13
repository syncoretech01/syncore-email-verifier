//go:build live

// Live MX tests reaching public DNS. Excluded from the default deterministic
// suite; run with: go test -tags=live ./...
package emailverifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckMxOK(t *testing.T) {
	domain := "github.com"

	mx, err := verifier.CheckMX(domain)
	assert.NoError(t, err)
	assert.True(t, mx.HasMXRecord)
	assert.Equal(t, mailHostSourceMX, mx.MailHostSource)
}

func TestCheckNoMxOK(t *testing.T) {
	domain := "githubexists.com"

	mx, err := verifier.CheckMX(domain)
	assert.Error(t, err, ErrNoSuchHost)
	assert.False(t, mx.HasMXRecord)
	assert.Equal(t, mailHostSourceNone, mx.MailHostSource)
}
