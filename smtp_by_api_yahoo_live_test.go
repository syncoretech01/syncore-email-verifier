//go:build live

// Live Yahoo API verifier test reaching login.yahoo.com. Unreliable by nature;
// excluded from the default deterministic suite. Run with: go test -tags=live ./...
package emailverifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestYahooCheckByAPI(t *testing.T) {
	yahooAPIVerifier := newYahooAPIVerifier(nil)
	t.Run("email exists", func(tt *testing.T) {
		res, err := yahooAPIVerifier.check("yahoo.com", "hello")
		assert.NoError(t, err)
		assert.Equal(t, true, res.HostExists)
		assert.Equal(t, true, res.Deliverable)
		assert.Equal(t, recipientAccepted, res.RecipientResult)
		assert.Equal(t, sourceAPI, res.Source)
	})
	t.Run("invalid email not exists", func(tt *testing.T) {
		res, err := yahooAPIVerifier.check("yahoo.com", "123")
		assert.NoError(t, err)
		assert.Equal(t, true, res.HostExists)
		assert.Equal(t, false, res.Deliverable)
		assert.Equal(t, recipientRejected, res.RecipientResult)
		assert.Equal(t, reasonMailboxNotFound, res.RecipientReason)
	})
}
