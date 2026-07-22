package verification

import (
	"net"
	"testing"

	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainHealth_DetectsRecords(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved()}
	txt := func(name string) ([]string, error) {
		switch name {
		case "example.com":
			return []string{"google-site-verification=xyz", "v=spf1 include:_spf.example.com ~all"}, nil
		case "_dmarc.example.com":
			return []string{"v=DMARC1; p=reject; rua=mailto:dmarc@example.com"}, nil
		}
		return nil, nil
	}
	a := run(t, e, WithSMTPEnabled(false), WithDomainHealth(true), WithTXTLookup(txt))

	require.NotNil(t, a.Domain.Health)
	assert.True(t, a.Domain.Health.SPF)
	assert.True(t, a.Domain.Health.DMARC)
	assert.True(t, a.Domain.Health.MX)
}

func TestDomainHealth_AbsentRecords(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved()}
	txt := func(string) ([]string, error) { return []string{"some-other-verification=abc"}, nil }
	a := run(t, e, WithSMTPEnabled(false), WithDomainHealth(true), WithTXTLookup(txt))

	require.NotNil(t, a.Domain.Health)
	assert.False(t, a.Domain.Health.SPF)
	assert.False(t, a.Domain.Health.DMARC)
	assert.True(t, a.Domain.Health.MX) // domain resolved to a usable mail host
}

func TestDomainHealth_DisabledLeavesNil(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved()}
	a := run(t, e, WithSMTPEnabled(false)) // domain health not enabled
	assert.Nil(t, a.Domain.Health)
}

func TestDomainHealth_SkippedWhenDomainDoesNotResolve(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mxErr: &net.DNSError{IsNotFound: true}}
	called := false
	txt := func(string) ([]string, error) { called = true; return nil, nil }
	a := run(t, e, WithSMTPEnabled(false), WithDomainHealth(true), WithTXTLookup(txt))

	assert.Nil(t, a.Domain.Health)
	assert.False(t, called, "TXT lookups must be skipped when the domain does not resolve")
}

func TestDomainHealth_LookupErrorTreatedAsAbsent(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved()}
	txt := func(string) ([]string, error) { return nil, errBoom }
	a := run(t, e, WithSMTPEnabled(false), WithDomainHealth(true), WithTXTLookup(txt))

	require.NotNil(t, a.Domain.Health)
	assert.False(t, a.Domain.Health.SPF)
	assert.False(t, a.Domain.Health.DMARC)
	assert.True(t, a.Domain.Health.MX)
}

var errBoom = &net.DNSError{Err: "server misbehaving", IsTemporary: true}

func TestComputeScoreComponents(t *testing.T) {
	cases := []struct {
		name string
		ev   classify.Evidence
		want ScoreComponents
	}{
		{
			"accepted good domain",
			classify.Evidence{SyntaxValid: true, DNS: classify.DNSResolved, MailHostSource: classify.MailHostMX, RecipientResult: classify.RecipientAccepted},
			ScoreComponents{Syntax: 100, Domain: 100, Mailbox: 100},
		},
		{
			"invalid syntax",
			classify.Evidence{SyntaxValid: false},
			ScoreComponents{Syntax: 0, Domain: 0, Mailbox: 40},
		},
		{
			"null mx rejects mail",
			classify.Evidence{SyntaxValid: true, DNS: classify.DNSResolved, NullMX: true},
			ScoreComponents{Syntax: 100, Domain: 0, Mailbox: 40},
		},
		{
			"disposable domain, rejected mailbox",
			classify.Evidence{SyntaxValid: true, DNS: classify.DNSResolved, MailHostSource: classify.MailHostMX, Disposable: true, RecipientResult: classify.RecipientRejected},
			ScoreComponents{Syntax: 100, Domain: 30, Mailbox: 0},
		},
		{
			"catch-all confirmed",
			classify.Evidence{SyntaxValid: true, DNS: classify.DNSResolved, MailHostSource: classify.MailHostMX, CatchAllResult: classify.CatchAllConfirmed},
			ScoreComponents{Syntax: 100, Domain: 100, Mailbox: 50},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, computeScoreComponents(tc.ev))
		})
	}
}

func TestDeliverabilityScore(t *testing.T) {
	cases := []struct {
		name       string
		status     classify.Status
		confidence int
		ev         classify.Evidence
		want       int
	}{
		{"invalid is zero", classify.StatusInvalid, 90, classify.Evidence{}, 0},
		{"valid is high", classify.StatusValid, 95, classify.Evidence{}, 95},
		{"unknown reflects confidence", classify.StatusUnknown, 10, classify.Evidence{}, 10},
		{"disposable capped low", classify.StatusRisky, 90, classify.Evidence{Disposable: true}, 15},
		{"catch-all capped mid", classify.StatusRisky, 50, classify.Evidence{CatchAllResult: classify.CatchAllConfirmed}, 50},
		{"role account reduced", classify.StatusRisky, 70, classify.Evidence{RoleAccount: true}, 60},
		{"valid with role penalty", classify.StatusValid, 95, classify.Evidence{RoleAccount: true}, 85},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deliverabilityScore(tc.status, tc.confidence, tc.ev))
		})
	}
}
