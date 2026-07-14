package verification

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedTime = time.Date(2026, 7, 13, 19, 5, 29, 0, time.UTC)

// stubEngine is a deterministic Engine that returns preset evidence and records
// how many times CheckMX / CheckSMTP were called.
type stubEngine struct {
	syntax     emailverifier.Syntax
	free       bool
	role       bool
	disposable bool
	suggestion string
	mx         *emailverifier.Mx
	mxErr      error
	smtp       *emailverifier.SMTP
	smtpErr    error

	mxCalls   int
	smtpCalls int
}

func (e *stubEngine) ParseAddress(string) emailverifier.Syntax { return e.syntax }
func (e *stubEngine) IsFreeDomain(string) bool                 { return e.free }
func (e *stubEngine) IsRoleAccount(string) bool                { return e.role }
func (e *stubEngine) IsDisposable(string) bool                 { return e.disposable }
func (e *stubEngine) SuggestDomain(string) string              { return e.suggestion }

func (e *stubEngine) CheckMX(string) (*emailverifier.Mx, error) {
	e.mxCalls++
	return e.mx, e.mxErr
}

func (e *stubEngine) CheckSMTP(string, string) (*emailverifier.SMTP, error) {
	e.smtpCalls++
	return e.smtp, e.smtpErr
}

func validSyntax() emailverifier.Syntax {
	return emailverifier.Syntax{Username: "person", Domain: "example.com", Valid: true}
}

func mxResolved() *emailverifier.Mx {
	return &emailverifier.Mx{HasMXRecord: true, MailHostSource: "mx"}
}

func run(t *testing.T, e *stubEngine, opts ...Option) Assessment {
	t.Helper()
	base := []Option{WithClock(func() time.Time { return fixedTime })}
	svc := NewService(e, append(base, opts...)...)
	return svc.Verify(context.Background(), "  person@example.com  ")
}

func TestService_Verify(t *testing.T) {
	cases := []struct {
		name       string
		engine     *stubEngine
		opts       []Option
		wantStatus classify.Status
		wantReason classify.ReasonCode
		wantSMTP   int // expected CheckSMTP call count
		extra      func(t *testing.T, a Assessment, e *stubEngine)
	}{
		{
			name:       "invalid syntax",
			engine:     &stubEngine{syntax: emailverifier.Syntax{Valid: false}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonSyntaxInvalid, wantSMTP: 0,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, 0, e.mxCalls, "MX must not be checked for invalid syntax")
				assert.Equal(t, "no", a.Result.Reachable)
			},
		},
		{
			name:       "dns nxdomain",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}, mxErr: &net.DNSError{IsNotFound: true}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonDomainNotFound, wantSMTP: 0,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, classify.CheckDNSFailure, a.SMTPCheckReason)
			},
		},
		{
			name:       "dns timeout",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}, mxErr: &net.DNSError{IsTimeout: true}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonDNSError, wantSMTP: 0,
		},
		{
			name:       "null mx",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{NullMX: true, MailHostSource: "null"}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonNullMX, wantSMTP: 0,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.True(t, a.Domain.NullMX)
				assert.Equal(t, classify.CheckNullMX, a.SMTPCheckReason)
			},
		},
		{
			name:       "no usable mail host",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonNoMailHost, wantSMTP: 0,
		},
		{
			name:       "implicit A host -> accepted",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{ImplicitMX: true, MailHostSource: "a"}, smtp: acceptedSMTP()},
			wantStatus: classify.StatusValid, wantReason: classify.ReasonSMTPAccepted, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.True(t, a.Domain.ImplicitMX)
				assert.Equal(t, "a", a.Domain.MailHostSource)
			},
		},
		{
			name:       "implicit AAAA host -> accepted",
			engine:     &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{ImplicitMX: true, MailHostSource: "aaaa"}, smtp: acceptedSMTP()},
			wantStatus: classify.StatusValid, wantReason: classify.ReasonSMTPAccepted, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, "aaaa", a.Domain.MailHostSource)
			},
		},
		{
			name:       "disposable short circuit",
			engine:     &stubEngine{syntax: validSyntax(), disposable: true, mx: mxResolved()},
			wantStatus: classify.StatusRisky, wantReason: classify.ReasonDisposableDomain, wantSMTP: 0,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, 1, e.mxCalls, "MX is still resolved for disposable domains")
				assert.Equal(t, classify.CheckDisposable, a.SMTPCheckReason)
				assert.Equal(t, "unknown", a.Result.Reachable)
			},
		},
		{
			name:       "smtp disabled",
			engine:     &stubEngine{syntax: validSyntax(), role: true, mx: mxResolved()},
			opts:       []Option{WithSMTPEnabled(false)},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonSMTPDisabled, wantSMTP: 0,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, classify.CheckDisabled, a.SMTPCheckReason)
			},
		},
		{
			name:       "accepted mailbox",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()},
			wantStatus: classify.StatusValid, wantReason: classify.ReasonSMTPAccepted, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, "yes", a.Result.Reachable)
				assert.Same(t, e.smtp, a.Result.SMTP)
				assert.Nil(t, a.Error)
				assert.Equal(t, fixedTime, a.CheckedAt)
				assert.Equal(t, classify.CheckAttempted, a.SMTPCheckReason)
			},
		},
		{
			name:       "accepted role mailbox",
			engine:     &stubEngine{syntax: validSyntax(), role: true, mx: mxResolved(), smtp: acceptedSMTP()},
			wantStatus: classify.StatusRisky, wantReason: classify.ReasonRoleAccount, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.True(t, a.Account.RoleAccount)
			},
		},
		{
			name:       "rejected nonexistent mailbox",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "rejected", RecipientReason: "mailbox_not_found", SMTPCode: 550, Source: "smtp"}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonMailboxRejected, wantSMTP: 1,
		},
		{
			name:       "disabled mailbox",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "rejected", RecipientReason: "mailbox_disabled", Disabled: true, SMTPCode: 550, Source: "smtp"}},
			wantStatus: classify.StatusRisky, wantReason: classify.ReasonMailboxDisabled, wantSMTP: 1,
		},
		{
			name:       "full inbox",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "temporary", RecipientReason: "full_inbox", FullInbox: true, SMTPCode: 452, Source: "smtp"}},
			wantStatus: classify.StatusRisky, wantReason: classify.ReasonFullInbox, wantSMTP: 1,
		},
		{
			name:       "catch-all confirmed",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{CatchAll: true, CatchAllResult: "confirmed", RecipientResult: "not_checked", Source: "smtp"}},
			wantStatus: classify.StatusRisky, wantReason: classify.ReasonCatchAll, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, classify.CheckCatchAll, a.SMTPCheckReason)
			},
		},
		{
			name:       "catch-all unknown",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{CatchAllResult: "unknown", RecipientResult: "unknown", Source: "smtp"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonSMTPInconclusive, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, classify.CheckAttempted, a.SMTPCheckReason)
			},
		},
		{
			name:       "temporary rejection",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "temporary", RecipientReason: "temporary_failure", SMTPCode: 450, Source: "smtp"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonTemporaryRejection, wantSMTP: 1,
		},
		{
			name:       "rate limiting",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "temporary", RecipientReason: "rate_limited", SMTPCode: 451, Source: "smtp"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonRateLimited, wantSMTP: 1,
		},
		{
			name:       "provider blocked",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "blocked", RecipientReason: "policy_block", SMTPCode: 554, Source: "smtp"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonProviderBlocked, wantSMTP: 1,
		},
		{
			name:       "smtp timeout with partial evidence",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{HostExists: true, RecipientResult: "unknown", Source: "smtp"}, smtpErr: &emailverifier.LookupError{Message: emailverifier.ErrTimeout, Details: "dial tcp 203.0.113.9:25: i/o timeout"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonSMTPTimeout, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				require.NotNil(t, a.SMTP)
				assert.True(t, a.SMTP.HostExists, "partial SMTP evidence must be preserved")
				assert.Equal(t, "unknown", a.Result.Reachable)
			},
		},
		{
			name:       "connection refused",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "not_checked", Source: "smtp"}, smtpErr: &emailverifier.LookupError{Message: emailverifier.ErrConnRefused, Details: "connect: connection refused"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonConnectionRefused, wantSMTP: 1,
		},
		{
			name:       "generic inconclusive smtp result",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "unknown", Source: "smtp"}},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonSMTPInconclusive, wantSMTP: 1,
		},
		{
			name:       "api accepted",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{HostExists: true, Deliverable: true, RecipientResult: "accepted", CatchAllResult: "not_checked", Source: "api"}},
			wantStatus: classify.StatusValid, wantReason: classify.ReasonSMTPAccepted, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, "api", a.Source)
				assert.Equal(t, classify.CheckAPIVerifier, a.SMTPCheckReason)
			},
		},
		{
			name:       "api nonexistent",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{HostExists: true, RecipientResult: "rejected", RecipientReason: "mailbox_not_found", Source: "api"}},
			wantStatus: classify.StatusInvalid, wantReason: classify.ReasonMailboxRejected, wantSMTP: 1,
		},
		{
			name:       "api error inconclusive",
			engine:     &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: &emailverifier.SMTP{RecipientResult: "unknown", CatchAllResult: "not_checked", Source: "api"}, smtpErr: errors.New("yahoo check by api, no cookies")},
			wantStatus: classify.StatusUnknown, wantReason: classify.ReasonSMTPInconclusive, wantSMTP: 1,
			extra: func(t *testing.T, a Assessment, e *stubEngine) {
				assert.Equal(t, classify.CheckAPIVerifier, a.SMTPCheckReason)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := run(t, tc.engine, tc.opts...)

			assert.Equal(t, tc.wantStatus, a.Status, "status")
			assert.Equal(t, tc.wantReason, a.ReasonCode, "reason_code")
			assert.Equal(t, tc.wantSMTP, tc.engine.smtpCalls, "CheckSMTP call count")

			// Metadata consistency: the assessment's status/retryable/confidence
			// must match the central metadata for its reason code.
			meta, ok := classify.Metadata(a.ReasonCode)
			require.True(t, ok)
			assert.Equal(t, meta.Status, a.Status)
			assert.Equal(t, meta.Retryable, a.Retryable)
			assert.Equal(t, meta.Confidence, a.Confidence)

			// Every assessment has a legacy Result and a fixed CheckedAt.
			require.NotNil(t, a.Result)
			assert.Equal(t, fixedTime, a.CheckedAt)
			assert.Equal(t, "person@example.com", a.Email, "surrounding whitespace must be trimmed")

			if tc.extra != nil {
				tc.extra(t, a, tc.engine)
			}
		})
	}
}

func acceptedSMTP() *emailverifier.SMTP {
	return &emailverifier.SMTP{
		HostExists:      true,
		Deliverable:     true,
		RecipientResult: "accepted",
		CatchAllResult:  "not_catch_all",
		SMTPCode:        250,
		Source:          "smtp",
	}
}

// TestService_ErrorIsSanitized proves the sanitized error never leaks raw SMTP
// server text, IP addresses, or proxy credentials from the underlying error.
func TestService_ErrorIsSanitized(t *testing.T) {
	e := &stubEngine{
		syntax: validSyntax(),
		mx:     mxResolved(),
		smtp:   &emailverifier.SMTP{RecipientResult: "unknown", Source: "smtp"},
		smtpErr: &emailverifier.LookupError{
			Message: emailverifier.ErrTimeout,
			Details: "dial tcp 203.0.113.9:25 via socks5://user:secret@10.0.0.1:1080: i/o timeout",
		},
	}
	a := run(t, e)

	require.NotNil(t, a.Error)
	assert.Equal(t, "smtp", a.Error.Code)
	assert.NotContains(t, a.Error.Message, "203.0.113.9")
	assert.NotContains(t, a.Error.Message, "socks5")
	assert.NotContains(t, a.Error.Message, "secret")
	assert.NotContains(t, a.Error.Message, "10.0.0.1")
	assert.Equal(t, "connection to the mail server timed out", a.Error.Message)
}

// TestService_ValidAndRiskyHaveNoError confirms successful/risky outcomes carry
// no error object.
func TestService_ValidAndRiskyHaveNoError(t *testing.T) {
	valid := run(t, &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()})
	assert.Nil(t, valid.Error)

	risky := run(t, &stubEngine{syntax: validSyntax(), disposable: true, mx: mxResolved()})
	assert.Nil(t, risky.Error)
}

// TestService_DefaultClockIsUTC verifies the production default clock is UTC.
func TestService_DefaultClockIsUTC(t *testing.T) {
	svc := NewService(&stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()})
	a := svc.Verify(context.Background(), "person@example.com")
	assert.Equal(t, time.UTC, a.CheckedAt.Location())
}
