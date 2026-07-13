//go:build live

// Live SMTP tests reaching public mail servers. Excluded from the default
// deterministic suite; run with: go test -tags=live ./...
package emailverifier

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCheckSMTPOK_ByApi(t *testing.T) {
	cases := []struct {
		name     string
		domain   string
		username string
		exists   bool
	}{
		{name: "yahoo exists", domain: "yahoo.com", username: "someone", exists: true},
		{name: "myyahoo exists", domain: "myyahoo.com", username: "someone", exists: true},
		{name: "yahoo no exists", domain: "yahoo.com", username: "123", exists: false},
		{name: "myyahoo no exists", domain: "myyahoo.com", username: "123", exists: false},
	}
	_ = verifier.EnableAPIVerifier(YAHOO)
	defer verifier.DisableAPIVerifier(YAHOO)
	for _, c := range cases {
		test := c
		t.Run(test.name, func(tt *testing.T) {
			smtp, err := verifier.CheckSMTP(test.domain, test.username)
			assert.NoError(t, err)
			assert.True(t, smtp.HostExists)
			assert.Equal(t, test.exists, smtp.Deliverable)
			assert.Equal(t, sourceAPI, smtp.Source)
		})
	}
}

func TestCheckSMTPOK_HostExists(t *testing.T) {
	domain := "github.com"

	smtp, err := verifier.CheckSMTP(domain, "")
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
	assert.True(t, smtp.CatchAll)
	assert.Equal(t, catchAllConfirmed, smtp.CatchAllResult)
}

func TestCheckSMTPOK_CatchAllHost(t *testing.T) {
	domain := "gmail.com"

	smtp, err := verifier.CheckSMTP(domain, "")
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
	assert.False(t, smtp.CatchAll)
}

func TestCheckSMTPOK_NoCatchAllHostCatchAllCheckDisabled(t *testing.T) {
	domain := "gmail.com"

	var v = NewVerifier().EnableSMTPCheck().DisableCatchAllCheck()
	smtp, err := v.CheckSMTP(domain, "")
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
	assert.Equal(t, catchAllNotChecked, smtp.CatchAllResult)
}

func TestCheckSMTPOK_UpdateFromEmail(t *testing.T) {
	domain := "github.com"
	verifier.FromEmail("from@email.top")

	smtp, err := verifier.CheckSMTP(domain, "")
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
}

func TestCheckSMTPOK_UpdateHelloName(t *testing.T) {
	domain := "github.com"
	verifier.HelloName("email.top")

	smtp, err := verifier.CheckSMTP(domain, "")
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
}

func TestCheckSMTPOK_WithNoExistUsername(t *testing.T) {
	domain := "github.com"
	username := "testing"

	smtp, err := verifier.CheckSMTP(domain, username)
	assert.NoError(t, err)
	assert.True(t, smtp.HostExists)
}

func TestCheckSMTPOK_HostNotExists(t *testing.T) {
	domain := "notExistHost.com"

	_, err := verifier.CheckSMTP(domain, "")
	assert.Error(t, err, ErrNoSuchHost)
}

func TestNewSMTPClientOK(t *testing.T) {
	domain := "gmail.com"
	v := NewVerifier()
	v.connectTimeout = 5 * time.Second
	v.operationTimeout = 5 * time.Second
	ret, _, err := v.newSMTPClient(domain)
	assert.NotNil(t, ret)
	assert.Nil(t, err)
}

func TestNewSMTPClientFailed_WithInvalidProxy(t *testing.T) {
	domain := "gmail.com"
	v := NewVerifier().Proxy("socks5://user:password@127.0.0.1:1080?timeout=5s")
	v.connectTimeout = 5 * time.Second
	v.operationTimeout = 5 * time.Second
	ret, _, err := v.newSMTPClient(domain)
	assert.Nil(t, ret)
	assert.Error(t, err, syscall.ECONNREFUSED)
}

func TestNewSMTPClientFailed(t *testing.T) {
	domain := "zzzz171777.com"
	v := NewVerifier()
	v.connectTimeout = 5 * time.Second
	v.operationTimeout = 5 * time.Second
	ret, _, err := v.newSMTPClient(domain)
	assert.Nil(t, ret)
	assert.Error(t, err)
}

func TestDialSMTPFailed_NoSuchHost(t *testing.T) {
	disposableDomain := "zzzzyyyyaaa123.com:25"
	timeout := 5 * time.Second
	ret, err := dialSMTP(disposableDomain, "", timeout, timeout)
	assert.Nil(t, ret)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no such host"))
}
