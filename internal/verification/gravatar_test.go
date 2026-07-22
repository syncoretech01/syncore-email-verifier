package verification

import (
	"net"
	"testing"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gravatarYes(url string) Option {
	return WithGravatarCheck(func(string) (bool, string) { return true, url })
}

func unknownEngine() *stubEngine {
	return &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "unknown", Source: "smtp"}}
}

func riskyCatchAllEngine() *stubEngine {
	return &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{CatchAll: true, CatchAllResult: "confirmed", RecipientResult: "not_checked", Source: "smtp"}}
}

func TestGravatar_BonusOnUnknown(t *testing.T) {
	base := run(t, unknownEngine())
	withG := run(t, unknownEngine(), gravatarYes("https://gravatar/x"))

	require.NotNil(t, withG.Account.Gravatar)
	assert.True(t, withG.Account.Gravatar.HasGravatar)
	assert.Equal(t, "https://gravatar/x", withG.Account.Gravatar.URL)
	// A public profile nudges an uncertain result up by exactly the bonus.
	assert.Equal(t, base.DeliverabilityScore+gravatarBonus, withG.DeliverabilityScore)
	// Legacy Result carries it too (for the GET presenter).
	require.NotNil(t, withG.Result.Gravatar)
	assert.True(t, withG.Result.Gravatar.HasGravatar)
	// The classification is untouched.
	assert.Equal(t, base.Status, withG.Status)
	assert.Equal(t, base.ReasonCode, withG.ReasonCode)
}

func TestGravatar_BonusOnRisky(t *testing.T) {
	base := run(t, riskyCatchAllEngine())
	withG := run(t, riskyCatchAllEngine(), gravatarYes("u"))
	assert.Equal(t, base.DeliverabilityScore+gravatarBonus, withG.DeliverabilityScore)
	assert.Equal(t, base.Status, withG.Status) // still risky
}

func TestGravatar_ValidUnchanged(t *testing.T) {
	valid := &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
	base := run(t, valid)
	valid2 := &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
	withG := run(t, valid2, gravatarYes("u"))
	// Evidence is attached, but a confident valid result is not inflated.
	require.NotNil(t, withG.Account.Gravatar)
	assert.Equal(t, base.DeliverabilityScore, withG.DeliverabilityScore)
}

func TestGravatar_NotCheckedForInvalid(t *testing.T) {
	invalid := &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}, mxErr: &net.DNSError{IsNotFound: true}}
	called := 0
	a := run(t, invalid, WithGravatarCheck(func(string) (bool, string) { called++; return true, "u" }))
	assert.Equal(t, 0, called, "no external Gravatar call for a definitively-invalid result")
	assert.Nil(t, a.Account.Gravatar)
	assert.Equal(t, 0, a.DeliverabilityScore)
}

func TestGravatar_NoProfileNoBonus(t *testing.T) {
	base := run(t, unknownEngine())
	withG := run(t, unknownEngine(), WithGravatarCheck(func(string) (bool, string) { return false, "" }))
	require.NotNil(t, withG.Account.Gravatar)
	assert.False(t, withG.Account.Gravatar.HasGravatar)
	assert.Equal(t, base.DeliverabilityScore, withG.DeliverabilityScore) // no bonus
}

func TestGravatar_ReputationStillCaps(t *testing.T) {
	// Gravatar bonus is applied first, then a poor bounce history caps the score.
	badRep := WithReputation(func(string) (DomainReputationEvidence, bool) {
		return DomainReputationEvidence{Delivered: 10, Bounced: 90, BounceRate: 0.9}, true
	})
	a := run(t, riskyCatchAllEngine(), gravatarYes("u"), badRep)
	assert.LessOrEqual(t, a.DeliverabilityScore, 20, "reputation cap must win over the gravatar bonus")
	assert.NotNil(t, a.Account.Gravatar)
}

func TestGravatar_DisabledByDefault(t *testing.T) {
	a := run(t, unknownEngine()) // no WithGravatarCheck
	assert.Nil(t, a.Account.Gravatar)
	assert.Nil(t, a.Result.Gravatar)
}
