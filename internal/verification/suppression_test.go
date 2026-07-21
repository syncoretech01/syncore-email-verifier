package verification

import (
	"testing"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestService_ReputationLowersScoreAndAttaches(t *testing.T) {
	e := &stubEngine{
		syntax: validSyntax(),
		mx:     mxResolved(),
		smtp:   &emailverifier.SMTP{RecipientResult: "accepted", CatchAllResult: "not_catch_all", Source: "smtp"},
	}
	// example.com has a very high bounce rate in the feedback history.
	rep := DomainReputationEvidence{Delivered: 10, Bounced: 90, BounceRate: 0.9}
	a := run(t, e, WithReputation(func(domain string) (DomainReputationEvidence, bool) {
		if domain == "example.com" {
			return rep, true
		}
		return DomainReputationEvidence{}, false
	}))

	require.NotNil(t, a.Domain.Reputation)
	assert.Equal(t, 90, a.Domain.Reputation.Bounced)
	assert.LessOrEqual(t, a.DeliverabilityScore, 20, "a >=50%% bounce rate caps the score at 20")
}

func TestService_NoReputationHistoryLeavesScore(t *testing.T) {
	e := &stubEngine{
		syntax: validSyntax(),
		mx:     mxResolved(),
		smtp:   &emailverifier.SMTP{RecipientResult: "accepted", CatchAllResult: "not_catch_all", Source: "smtp"},
	}
	a := run(t, e, WithReputation(func(string) (DomainReputationEvidence, bool) {
		return DomainReputationEvidence{}, false
	}))
	assert.Nil(t, a.Domain.Reputation)
	assert.Equal(t, 95, a.DeliverabilityScore, "no history -> unchanged valid score")
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
