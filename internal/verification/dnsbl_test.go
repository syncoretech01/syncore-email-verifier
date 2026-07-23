package verification

import (
	"errors"
	"net"
	"testing"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEngine() *stubEngine {
	return &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
}

func TestDNSBL_Blocklisted_CapsScore(t *testing.T) {
	base := run(t, validEngine()) // valid -> high score
	a := run(t, validEngine(), WithDNSBLCheck(func(string) (bool, error) { return true, nil }))

	require.NotNil(t, a.Domain.Blocklisted)
	assert.True(t, *a.Domain.Blocklisted)
	assert.Equal(t, dnsblBlockedScore, a.DeliverabilityScore)       // capped to 15
	assert.Less(t, a.DeliverabilityScore, base.DeliverabilityScore) // strictly lower
	assert.Equal(t, base.Status, a.Status)                          // classification unchanged
	assert.Equal(t, base.ReasonCode, a.ReasonCode)
}

func TestDNSBL_NotListed_NoChange(t *testing.T) {
	base := run(t, validEngine())
	a := run(t, validEngine(), WithDNSBLCheck(func(string) (bool, error) { return false, nil }))
	require.NotNil(t, a.Domain.Blocklisted)
	assert.False(t, *a.Domain.Blocklisted)
	assert.Equal(t, base.DeliverabilityScore, a.DeliverabilityScore)
}

func TestDNSBL_LookupError_NotAttached(t *testing.T) {
	a := run(t, validEngine(), WithDNSBLCheck(func(string) (bool, error) { return false, errors.New("dns timeout") }))
	assert.Nil(t, a.Domain.Blocklisted, "a failed lookup must not attach evidence or affect the score")
}

func TestDNSBL_DisabledByDefault(t *testing.T) {
	a := run(t, validEngine())
	assert.Nil(t, a.Domain.Blocklisted)
}

func TestDNSBL_NotRunForUnresolvedDomain(t *testing.T) {
	called := 0
	// domain_not_found -> DNS not resolved -> no DNSBL lookup, no wasted DNS call.
	invalid := &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}, mxErr: &net.DNSError{IsNotFound: true}}
	a := run(t, invalid, WithDNSBLCheck(func(string) (bool, error) { called++; return true, nil }))
	assert.Equal(t, 0, called)
	assert.Nil(t, a.Domain.Blocklisted)
}
