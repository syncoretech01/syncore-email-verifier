package verification

import (
	"testing"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/stretchr/testify/assert"
)

func TestService_SuppressedShortCircuitsBeforeNetwork(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved()}
	a := run(t, e, WithSuppressionCheck(func(string) bool { return true }))

	assert.True(t, a.Suppressed)
	assert.Equal(t, classify.StatusRisky, a.Status)
	assert.NotNil(t, a.Error)
	assert.Equal(t, "policy", a.Error.Code)
	assert.Equal(t, 0, e.mxCalls, "no MX lookup for a suppressed address")
	assert.Equal(t, 0, e.smtpCalls, "no SMTP check for a suppressed address")
}

func TestService_NotSuppressedProceeds(t *testing.T) {
	e := &stubEngine{
		syntax: validSyntax(),
		mx:     mxResolved(),
		smtp:   &emailverifier.SMTP{RecipientResult: "accepted", CatchAllResult: "not_catch_all", Source: "smtp"},
	}
	a := run(t, e, WithSuppressionCheck(func(string) bool { return false }))

	assert.False(t, a.Suppressed)
	assert.Greater(t, e.mxCalls, 0, "a non-suppressed address is verified normally")
}
