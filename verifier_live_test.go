//go:build live

// These tests reach public DNS / SMTP / HTTP services and are intentionally
// excluded from the default deterministic suite. Run them with: go test -tags=live ./...
//
// Assertions here focus on the stable legacy fields; the additive recipient /
// catch-all / source evidence introduced in Phase 1A depends on live server
// behavior and is covered deterministically by the in-process fake SMTP suite.
package emailverifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckEmailOK_SMTPHostNotExists(t *testing.T) {
	var (
		username = "email_username"
		domain   = "domainnotexists.com"
		email    = username + "@" + domain
	)

	ret, err := verifier.Verify(email)
	assert.ErrorContains(t, err, ErrNoSuchHost)
	assert.Equal(t, reachableNo, ret.Reachable)
	assert.False(t, ret.HasMxRecords)
	assert.Nil(t, ret.SMTP)
}

func TestCheckEmailOK_SMTPHostExists_NotCatchAll(t *testing.T) {
	var (
		username = "email_username"
		domain   = "github.com"
		email    = username + "@" + domain
	)

	ret, err := verifier.Verify(email)
	assert.NoError(t, err)
	assert.True(t, ret.HasMxRecords)
	assert.NotNil(t, ret.SMTP)
	assert.True(t, ret.SMTP.HostExists)
}

func TestCheckEmailOK_SMTPHostExists_FreeDomain(t *testing.T) {
	var (
		username = "email_username"
		domain   = "gmail.com"
		email    = username + "@" + domain
	)

	ret, err := verifier.Verify(email)
	assert.NoError(t, err)
	assert.True(t, ret.HasMxRecords)
	assert.True(t, ret.Free)
	assert.NotNil(t, ret.SMTP)
}

func TestCheckEmail_RoleAccount(t *testing.T) {
	var (
		username = "admin"
		domain   = "github.com"
		email    = username + "@" + domain
	)

	ret, err := verifier.Verify(email)
	assert.NoError(t, err)
	assert.True(t, ret.RoleAccount)
	assert.True(t, ret.HasMxRecords)
}

func TestCheckEmail_DisabledSMTPCheck(t *testing.T) {
	var (
		username = "email_username"
		domain   = "randomain.com"
		email    = username + "@" + domain
	)

	verifier.DisableSMTPCheck()
	ret, err := verifier.Verify(email)
	verifier.EnableSMTPCheck()

	assert.NoError(t, err)
	assert.True(t, ret.HasMxRecords)
	assert.Nil(t, ret.SMTP)
	assert.Equal(t, reachableUnknown, ret.Reachable)
}

func TestNewVerifierOK_AutoUpdateDisposable(t *testing.T) {
	verifier.EnableAutoUpdateDisposable()
}

func TestNewVerifierOK_EnableAutoUpdateDisposable(t *testing.T) {
	verifier.EnableAutoUpdateDisposable()
}

func TestNewVerifierOK_AutoUpdateDisposableDuplicate(t *testing.T) {
	verifier.DisableAutoUpdateDisposable()

	verifier.EnableAutoUpdateDisposable()
	verifier.DisableAutoUpdateDisposable()

	verifier.EnableAutoUpdateDisposable()
	verifier.DisableAutoUpdateDisposable()
	verifier.EnableAutoUpdateDisposable()
}

func TestStopCurrentScheduleOK(t *testing.T) {
	verifier.EnableAutoUpdateDisposable()
	verifier.stopCurrentSchedule()
}

func TestCheckEmail_EnableDomainSuggest(t *testing.T) {
	var (
		username = "email_username"
		domain   = "hotmail.com"
		email    = username + "@" + domain
	)

	ret, _ := verifier.Verify(email)

	assert.Empty(t, ret.Suggestion)
}

func TestCheckEmail_EnableDomainSuggest_Gmail(t *testing.T) {
	var (
		username = "email_username"
		domain   = "gmai.com"
		email    = username + "@" + domain
	)

	ret, _ := verifier.EnableDomainSuggest().Verify(email)

	assert.Equal(t, "gmail.com", ret.Suggestion)
}
