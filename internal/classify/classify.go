package classify

// The typed string enums below mirror the values the Phase 1A engine emits on
// emailverifier.SMTP / emailverifier.Mx. They are duplicated here (rather than
// imported) so this package stays pure and dependency-free; the verification
// service converts engine strings into these types when building Evidence.

// DNSOutcome is the normalized result of DNS/MX resolution.
type DNSOutcome string

const (
	DNSResolved  DNSOutcome = "resolved"
	DNSNotFound  DNSOutcome = "not_found"
	DNSTempError DNSOutcome = "temp_error"
)

// SMTPCheckReason explains whether/why the SMTP recipient check ran.
type SMTPCheckReason string

const (
	CheckAttempted   SMTPCheckReason = "attempted"
	CheckDisabled    SMTPCheckReason = "disabled"
	CheckDisposable  SMTPCheckReason = "disposable"
	CheckCatchAll    SMTPCheckReason = "catch_all"
	CheckDNSFailure  SMTPCheckReason = "dns_failure"
	CheckNoMailHost  SMTPCheckReason = "no_mail_host"
	CheckNullMX      SMTPCheckReason = "null_mx"
	CheckAPIVerifier SMTPCheckReason = "api_verifier"
)

// Source is the verification source.
type Source string

const (
	SourceSMTP Source = "smtp"
	SourceAPI  Source = "api"
	SourceNone Source = ""
)

// Transport is the normalized transport-level signal derived from a CheckSMTP
// error (never from arbitrary user-visible strings).
type Transport string

const (
	TransportNone    Transport = ""
	TransportTimeout Transport = "timeout"
	TransportRefused Transport = "refused"
	TransportOther   Transport = "other"
)

// MailHostSource mirrors emailverifier.Mx.MailHostSource.
type MailHostSource string

const (
	MailHostMX   MailHostSource = "mx"
	MailHostA    MailHostSource = "a"
	MailHostAAAA MailHostSource = "aaaa"
	MailHostNull MailHostSource = "null"
	MailHostNone MailHostSource = "none"
)

// RecipientResult mirrors emailverifier.SMTP.RecipientResult.
type RecipientResult string

const (
	RecipientAccepted   RecipientResult = "accepted"
	RecipientRejected   RecipientResult = "rejected"
	RecipientTemporary  RecipientResult = "temporary"
	RecipientBlocked    RecipientResult = "blocked"
	RecipientUnknown    RecipientResult = "unknown"
	RecipientNotChecked RecipientResult = "not_checked"
)

// RecipientReason mirrors emailverifier.SMTP.RecipientReason.
type RecipientReason string

const (
	RecipReasonNone            RecipientReason = ""
	RecipReasonMailboxNotFound RecipientReason = "mailbox_not_found"
	RecipReasonMailboxDisabled RecipientReason = "mailbox_disabled"
	RecipReasonFullInbox       RecipientReason = "full_inbox"
	RecipReasonPolicyBlock     RecipientReason = "policy_block"
	RecipReasonRateLimited     RecipientReason = "rate_limited"
	RecipReasonTemporaryFail   RecipientReason = "temporary_failure"
	RecipReasonGreylisted      RecipientReason = "greylisted"
)

// CatchAllResult mirrors emailverifier.SMTP.CatchAllResult.
type CatchAllResult string

const (
	CatchAllConfirmed  CatchAllResult = "confirmed"
	CatchAllNot        CatchAllResult = "not_catch_all"
	CatchAllUnknown    CatchAllResult = "unknown"
	CatchAllNotChecked CatchAllResult = "not_checked"
)

// Evidence is the complete, normalized input to the classifier.
type Evidence struct {
	Email       string
	SyntaxValid bool

	DNS DNSOutcome

	// MX evidence.
	HasMXRecords   bool
	NullMX         bool
	MailHostSource MailHostSource

	Disposable  bool
	RoleAccount bool
	Free        bool
	Suggestion  string

	// SMTP / recipient evidence.
	SMTPAttempted   bool
	SMTPCheckReason SMTPCheckReason
	Source          Source
	RecipientResult RecipientResult
	RecipientReason RecipientReason
	CatchAllResult  CatchAllResult
	SMTPCode        int

	// Normalized transport-level signal derived from the CheckSMTP error.
	Transport Transport
}

// Classification is the classifier's output.
type Classification struct {
	Status     Status
	ReasonCode ReasonCode
	Retryable  bool
	Confidence int
}

// Classify maps evidence to a single verification outcome. It is a pure
// function: deterministic, no side effects, no I/O.
func Classify(ev Evidence) Classification {
	rc := classifyReason(ev)
	meta := reasonMeta[rc] // every reason classifyReason can return has metadata
	return Classification{
		Status:     meta.Status,
		ReasonCode: rc,
		Retryable:  meta.Retryable,
		Confidence: meta.Confidence,
	}
}

// classifyReason applies the approved precedence ladder (docs/PHASE_1_PLAN.md §4)
// and returns the first matching reason code.
func classifyReason(ev Evidence) ReasonCode {
	// 1. Syntax invalid.
	if !ev.SyntaxValid {
		return ReasonSyntaxInvalid
	}
	// 2. DNS: domain does not exist.
	if ev.DNS == DNSNotFound {
		return ReasonDomainNotFound
	}
	// 3. DNS: temporary failure.
	if ev.DNS == DNSTempError {
		return ReasonDNSError
	}
	// 4. Null MX (RFC 7505).
	if ev.NullMX {
		return ReasonNullMX
	}
	// 5. No usable mail host (no MX and no A/AAAA).
	if ev.MailHostSource == MailHostNone {
		return ReasonNoMailHost
	}
	// 6. Disposable domain.
	if ev.Disposable {
		return ReasonDisposableDomain
	}
	// 7. Explicit nonexistent recipient.
	if ev.RecipientReason == RecipReasonMailboxNotFound {
		return ReasonMailboxRejected
	}
	// 8. Explicit disabled mailbox.
	if ev.RecipientReason == RecipReasonMailboxDisabled {
		return ReasonMailboxDisabled
	}
	// 9. Full inbox / over quota.
	if ev.RecipientReason == RecipReasonFullInbox {
		return ReasonFullInbox
	}
	// 10. Confirmed catch-all.
	if ev.CatchAllResult == CatchAllConfirmed {
		return ReasonCatchAll
	}
	// 11 & 12. Accepted recipient (role account downgrades to risky).
	if ev.RecipientResult == RecipientAccepted {
		if ev.RoleAccount {
			return ReasonRoleAccount
		}
		return ReasonSMTPAccepted
	}
	// 13. Temporary recipient failure / greylisting.
	if ev.RecipientResult == RecipientTemporary &&
		(ev.RecipientReason == RecipReasonTemporaryFail || ev.RecipientReason == RecipReasonGreylisted) {
		return ReasonTemporaryRejection
	}
	// 14. Rate limiting.
	if ev.RecipientReason == RecipReasonRateLimited {
		return ReasonRateLimited
	}
	// 15. Policy / provider blocking.
	if ev.RecipientResult == RecipientBlocked {
		return ReasonProviderBlocked
	}
	// 16. Transport timeout.
	if ev.Transport == TransportTimeout {
		return ReasonSMTPTimeout
	}
	// 17. Connection refused.
	if ev.Transport == TransportRefused {
		return ReasonConnectionRefused
	}
	// 18. SMTP disabled by configuration (never inferred from not_checked alone).
	if ev.SMTPCheckReason == CheckDisabled {
		return ReasonSMTPDisabled
	}
	// 19. Any remaining inconclusive completed check.
	return ReasonSMTPInconclusive
}
