package emailverifier

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// newNoMailHostVerifier returns a verifier whose resolvers report a domain that
// resolves to no usable mail host, so Verify exercises the pre-SMTP stages
// (including domain suggestion) deterministically without any network access.
func newNoMailHostVerifier() *Verifier {
	v := NewVerifier().EnableSMTPCheck()
	v.lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	v.lookupIP = func(string) ([]net.IP, error) { return nil, nil }
	return v
}

func TestVerify_DomainSuggest_Enabled(t *testing.T) {
	v := newNoMailHostVerifier().EnableDomainSuggest()
	ret, err := v.Verify("user@gmai.com")
	assert.NoError(t, err)
	assert.Equal(t, "gmail.com", ret.Suggestion)
}

func TestVerify_DomainSuggest_Disabled(t *testing.T) {
	v := newNoMailHostVerifier().DisableDomainSuggest()
	ret, err := v.Verify("user@gmai.com")
	assert.NoError(t, err)
	assert.Empty(t, ret.Suggestion)
}

func TestCheckEmail_ErrorSyntax(t *testing.T) {
	var (
		username = ""
		domain   = "yahoo.com"
		address  = username + "@" + domain
		email    = address
	)

	ret, err := verifier.Verify(email)
	expected := Result{
		Email: email,
		Syntax: Syntax{
			Username: username,
			Domain:   "",
			Valid:    false,
		},
		HasMxRecords: false,
		Reachable:    reachableUnknown,
		Disposable:   false,
		RoleAccount:  false,
		Free:         false,
		SMTP:         nil,
	}
	assert.Nil(t, err)
	assert.Equal(t, &expected, ret)
}

func TestCheckEmail_Disposable(t *testing.T) {
	var (
		username = "exampleuser"
		domain   = "zzjbfwqi.shop"
		address  = username + "@" + domain
		email    = address
	)

	ret, err := verifier.Verify(email)
	expected := Result{
		Email: email,
		Syntax: Syntax{
			Username: username,
			Domain:   domain,
			Valid:    true,
		},
		HasMxRecords: false,
		Reachable:    reachableUnknown,
		Disposable:   true,
		RoleAccount:  false,
		Free:         false,
		SMTP:         nil,
	}
	assert.Nil(t, err)
	assert.Equal(t, &expected, ret)
}

func TestCheckEmail_Disposable_override(t *testing.T) {
	var (
		username = "exampleuser"
		domain   = "iamdisposableemail.test"
		address  = username + "@" + domain
		email    = address
	)

	verifier := NewVerifier().EnableSMTPCheck().AddDisposableDomains([]string{"iamdisposableemail.test"})
	ret, err := verifier.Verify(email)
	expected := Result{
		Email: email,
		Syntax: Syntax{
			Username: username,
			Domain:   domain,
			Valid:    true,
		},
		HasMxRecords: false,
		Reachable:    reachableUnknown,
		Disposable:   true,
		RoleAccount:  false,
		Free:         false,
		SMTP:         nil,
	}
	assert.Nil(t, err)
	assert.Equal(t, &expected, ret)
}

func TestStopCurrentSchedule_ScheduleIsNil(t *testing.T) {
	verifier.schedule = nil
	verifier.stopCurrentSchedule()
}
