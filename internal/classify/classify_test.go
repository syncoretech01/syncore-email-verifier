package classify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Metadata completeness ---

func TestReasonMetadata_CompleteAndConsistent(t *testing.T) {
	codes := AllReasonCodes()

	// Every reason code has exactly one metadata record with a valid status and
	// a confidence in range.
	validStatus := map[Status]bool{StatusValid: true, StatusInvalid: true, StatusRisky: true, StatusUnknown: true}
	for _, rc := range codes {
		m, ok := Metadata(rc)
		require.Truef(t, ok, "missing metadata for reason code %q", rc)
		assert.Truef(t, validStatus[m.Status], "reason %q has invalid status %q", rc, m.Status)
		assert.GreaterOrEqual(t, m.Confidence, 0, "reason %q confidence < 0", rc)
		assert.LessOrEqual(t, m.Confidence, 100, "reason %q confidence > 100", rc)
	}

	// No extra or missing entries: AllReasonCodes and reasonMeta agree exactly.
	assert.Equal(t, len(codes), len(reasonMeta), "AllReasonCodes and reasonMeta size mismatch")
	seen := map[ReasonCode]bool{}
	for _, rc := range codes {
		assert.Falsef(t, seen[rc], "duplicate reason code %q in AllReasonCodes", rc)
		seen[rc] = true
	}
	for rc := range reasonMeta {
		assert.Truef(t, seen[rc], "reasonMeta has code %q not listed in AllReasonCodes", rc)
	}

	// internal_error must never be a verification reason code.
	_, ok := reasonMeta[ReasonCode("internal_error")]
	assert.False(t, ok, "internal_error must not be a verification reason code")
}

func TestClassify_NeverInternalErrorAndAlwaysHasMetadata(t *testing.T) {
	// A deliberately empty/ambiguous evidence must still resolve to a known,
	// metadata-backed reason (smtp_inconclusive), never fall through.
	c := Classify(Evidence{SyntaxValid: true})
	assert.Equal(t, ReasonSMTPInconclusive, c.ReasonCode)
	assert.NotEqual(t, ReasonCode("internal_error"), c.ReasonCode)
	_, ok := Metadata(c.ReasonCode)
	assert.True(t, ok)
}

// --- Precedence & conflicts ---

func TestClassify_Precedence(t *testing.T) {
	cases := []struct {
		name       string
		ev         Evidence
		wantStatus Status
		wantReason ReasonCode
		wantRetry  bool
		wantConf   int
	}{
		{
			name:       "syntax invalid",
			ev:         Evidence{SyntaxValid: false},
			wantStatus: StatusInvalid, wantReason: ReasonSyntaxInvalid, wantRetry: false, wantConf: 100,
		},
		{
			name:       "dns not found",
			ev:         Evidence{SyntaxValid: true, DNS: DNSNotFound},
			wantStatus: StatusInvalid, wantReason: ReasonDomainNotFound, wantRetry: false, wantConf: 99,
		},
		{
			name:       "dns temp error beats disposable",
			ev:         Evidence{SyntaxValid: true, DNS: DNSTempError, Disposable: true},
			wantStatus: StatusUnknown, wantReason: ReasonDNSError, wantRetry: true, wantConf: 10,
		},
		{
			name:       "null mx beats A evidence",
			ev:         Evidence{SyntaxValid: true, DNS: DNSResolved, NullMX: true, MailHostSource: MailHostA},
			wantStatus: StatusInvalid, wantReason: ReasonNullMX, wantRetry: false, wantConf: 99,
		},
		{
			name:       "no mail host",
			ev:         Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostNone},
			wantStatus: StatusInvalid, wantReason: ReasonNoMailHost, wantRetry: false, wantConf: 95,
		},
		{
			name:       "disposable plus role -> disposable",
			ev:         Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX, Disposable: true, RoleAccount: true},
			wantStatus: StatusRisky, wantReason: ReasonDisposableDomain, wantRetry: false, wantConf: 90,
		},
		{
			name: "rejected mailbox plus role -> mailbox_rejected",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX, RoleAccount: true,
				RecipientResult: RecipientRejected, RecipientReason: RecipReasonMailboxNotFound, SMTPCode: 550},
			wantStatus: StatusInvalid, wantReason: ReasonMailboxRejected, wantRetry: false, wantConf: 90,
		},
		{
			name: "mailbox disabled",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientRejected, RecipientReason: RecipReasonMailboxDisabled, SMTPCode: 550},
			wantStatus: StatusRisky, wantReason: ReasonMailboxDisabled, wantRetry: false, wantConf: 80,
		},
		{
			name: "full inbox with temporary result",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientTemporary, RecipientReason: RecipReasonFullInbox, SMTPCode: 452},
			wantStatus: StatusRisky, wantReason: ReasonFullInbox, wantRetry: true, wantConf: 60,
		},
		{
			name: "accepted plus catch-all confirmed -> catch_all",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				CatchAllResult: CatchAllConfirmed, RecipientResult: RecipientAccepted},
			wantStatus: StatusRisky, wantReason: ReasonCatchAll, wantRetry: false, wantConf: 50,
		},
		{
			name: "accepted plus role -> role_account",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RoleAccount: true, RecipientResult: RecipientAccepted},
			wantStatus: StatusRisky, wantReason: ReasonRoleAccount, wantRetry: false, wantConf: 70,
		},
		{
			name: "accepted -> smtp_accepted",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				CatchAllResult: CatchAllNot, RecipientResult: RecipientAccepted, SMTPCode: 250, Source: SourceSMTP},
			wantStatus: StatusValid, wantReason: ReasonSMTPAccepted, wantRetry: false, wantConf: 95,
		},
		{
			name: "temporary failure -> temporary_rejection",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientTemporary, RecipientReason: RecipReasonTemporaryFail, SMTPCode: 450},
			wantStatus: StatusUnknown, wantReason: ReasonTemporaryRejection, wantRetry: true, wantConf: 20,
		},
		{
			name: "greylisted -> temporary_rejection",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientTemporary, RecipientReason: RecipReasonGreylisted, SMTPCode: 421},
			wantStatus: StatusUnknown, wantReason: ReasonTemporaryRejection, wantRetry: true, wantConf: 20,
		},
		{
			name: "rate limited",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientTemporary, RecipientReason: RecipReasonRateLimited, SMTPCode: 451},
			wantStatus: StatusUnknown, wantReason: ReasonRateLimited, wantRetry: true, wantConf: 15,
		},
		{
			name: "provider blocked with smtp code",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RecipientResult: RecipientBlocked, RecipientReason: RecipReasonPolicyBlock, SMTPCode: 554},
			wantStatus: StatusUnknown, wantReason: ReasonProviderBlocked, wantRetry: true, wantConf: 15,
		},
		{
			name: "transport timeout with partial evidence",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				SMTPAttempted: true, SMTPCheckReason: CheckAttempted, RecipientResult: RecipientUnknown, Transport: TransportTimeout},
			wantStatus: StatusUnknown, wantReason: ReasonSMTPTimeout, wantRetry: true, wantConf: 10,
		},
		{
			name: "connection refused",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				SMTPAttempted: true, SMTPCheckReason: CheckAttempted, RecipientResult: RecipientNotChecked, Transport: TransportRefused},
			wantStatus: StatusUnknown, wantReason: ReasonConnectionRefused, wantRetry: true, wantConf: 10,
		},
		{
			name: "smtp disabled plus role -> smtp_disabled",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				RoleAccount: true, SMTPCheckReason: CheckDisabled, RecipientResult: RecipientNotChecked},
			wantStatus: StatusUnknown, wantReason: ReasonSMTPDisabled, wantRetry: false, wantConf: 30,
		},
		{
			name: "catch-all unknown plus recipient unknown -> smtp_inconclusive",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				SMTPAttempted: true, SMTPCheckReason: CheckAttempted, CatchAllResult: CatchAllUnknown, RecipientResult: RecipientUnknown},
			wantStatus: StatusUnknown, wantReason: ReasonSMTPInconclusive, wantRetry: true, wantConf: 5,
		},
		{
			name: "api accepted -> smtp_accepted",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				Source: SourceAPI, SMTPCheckReason: CheckAPIVerifier, RecipientResult: RecipientAccepted},
			wantStatus: StatusValid, wantReason: ReasonSMTPAccepted, wantRetry: false, wantConf: 95,
		},
		{
			name: "api nonexistent -> mailbox_rejected",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				Source: SourceAPI, SMTPCheckReason: CheckAPIVerifier, RecipientResult: RecipientRejected, RecipientReason: RecipReasonMailboxNotFound},
			wantStatus: StatusInvalid, wantReason: ReasonMailboxRejected, wantRetry: false, wantConf: 90,
		},
		{
			name: "api inconclusive -> smtp_inconclusive",
			ev: Evidence{SyntaxValid: true, DNS: DNSResolved, MailHostSource: MailHostMX,
				Source: SourceAPI, SMTPCheckReason: CheckAPIVerifier, RecipientResult: RecipientUnknown},
			wantStatus: StatusUnknown, wantReason: ReasonSMTPInconclusive, wantRetry: true, wantConf: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.ev)
			assert.Equal(t, tc.wantStatus, got.Status, "status")
			assert.Equal(t, tc.wantReason, got.ReasonCode, "reason_code")
			assert.Equal(t, tc.wantRetry, got.Retryable, "retryable")
			assert.Equal(t, tc.wantConf, got.Confidence, "confidence")
		})
	}
}
