//go:build live

// This test performs a real DNS resolution against a bogus host and is therefore
// excluded from the default deterministic suite. Run with: go test -tags=live ./...
package emailverifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpdateDisposableDomainsFailed_NoSuchHost(t *testing.T) {
	err := updateDisposableDomains("http://abcmockxyz.aaa")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no such host")
}
