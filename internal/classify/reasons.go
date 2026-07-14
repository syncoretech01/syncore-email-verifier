// Package classify turns granular engine evidence into a single verification
// outcome. It is pure: no network, environment, logging, or clock access.
package classify

// Status is a verification status. Only the four values below are valid.
type Status string

const (
	StatusValid   Status = "valid"
	StatusInvalid Status = "invalid"
	StatusRisky   Status = "risky"
	StatusUnknown Status = "unknown"
)

// ReasonCode is a Phase 1 verification reason code.
//
// Note: internal_error is intentionally NOT a verification reason code. It is
// reserved for a later HTTP 500 response and must never be produced here.
type ReasonCode string

const (
	ReasonSMTPAccepted       ReasonCode = "smtp_accepted"
	ReasonSyntaxInvalid      ReasonCode = "syntax_invalid"
	ReasonDomainNotFound     ReasonCode = "domain_not_found"
	ReasonNullMX             ReasonCode = "null_mx"
	ReasonNoMailHost         ReasonCode = "no_mail_host"
	ReasonMailboxRejected    ReasonCode = "mailbox_rejected"
	ReasonDisposableDomain   ReasonCode = "disposable_domain"
	ReasonMailboxDisabled    ReasonCode = "mailbox_disabled"
	ReasonRoleAccount        ReasonCode = "role_account"
	ReasonFullInbox          ReasonCode = "full_inbox"
	ReasonCatchAll           ReasonCode = "catch_all"
	ReasonTemporaryRejection ReasonCode = "temporary_rejection"
	ReasonRateLimited        ReasonCode = "rate_limited"
	ReasonProviderBlocked    ReasonCode = "provider_blocked"
	ReasonDNSError           ReasonCode = "dns_error"
	ReasonSMTPTimeout        ReasonCode = "smtp_timeout"
	ReasonConnectionRefused  ReasonCode = "connection_refused"
	ReasonSMTPInconclusive   ReasonCode = "smtp_inconclusive"
	ReasonSMTPDisabled       ReasonCode = "smtp_disabled"
)

// Meta is the centralized metadata for a reason code.
type Meta struct {
	Status     Status
	Retryable  bool
	Confidence int // 0..100
}

// reasonMeta is the single source of truth for status/retryable/confidence.
// Values are the approved calibration from docs/PHASE_1_PLAN.md §5.
var reasonMeta = map[ReasonCode]Meta{
	ReasonSMTPAccepted:       {Status: StatusValid, Retryable: false, Confidence: 95},
	ReasonSyntaxInvalid:      {Status: StatusInvalid, Retryable: false, Confidence: 100},
	ReasonDomainNotFound:     {Status: StatusInvalid, Retryable: false, Confidence: 99},
	ReasonNullMX:             {Status: StatusInvalid, Retryable: false, Confidence: 99},
	ReasonNoMailHost:         {Status: StatusInvalid, Retryable: false, Confidence: 95},
	ReasonMailboxRejected:    {Status: StatusInvalid, Retryable: false, Confidence: 90},
	ReasonDisposableDomain:   {Status: StatusRisky, Retryable: false, Confidence: 90},
	ReasonMailboxDisabled:    {Status: StatusRisky, Retryable: false, Confidence: 80},
	ReasonRoleAccount:        {Status: StatusRisky, Retryable: false, Confidence: 70},
	ReasonFullInbox:          {Status: StatusRisky, Retryable: true, Confidence: 60},
	ReasonCatchAll:           {Status: StatusRisky, Retryable: false, Confidence: 50},
	ReasonTemporaryRejection: {Status: StatusUnknown, Retryable: true, Confidence: 20},
	ReasonRateLimited:        {Status: StatusUnknown, Retryable: true, Confidence: 15},
	ReasonProviderBlocked:    {Status: StatusUnknown, Retryable: true, Confidence: 15},
	ReasonDNSError:           {Status: StatusUnknown, Retryable: true, Confidence: 10},
	ReasonSMTPTimeout:        {Status: StatusUnknown, Retryable: true, Confidence: 10},
	ReasonConnectionRefused:  {Status: StatusUnknown, Retryable: true, Confidence: 10},
	ReasonSMTPInconclusive:   {Status: StatusUnknown, Retryable: true, Confidence: 5},
	ReasonSMTPDisabled:       {Status: StatusUnknown, Retryable: false, Confidence: 30},
}

// AllReasonCodes returns every Phase 1 verification reason code exactly once.
// This is the canonical enumeration used to prove metadata completeness.
func AllReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonSMTPAccepted,
		ReasonSyntaxInvalid,
		ReasonDomainNotFound,
		ReasonNullMX,
		ReasonNoMailHost,
		ReasonMailboxRejected,
		ReasonDisposableDomain,
		ReasonMailboxDisabled,
		ReasonRoleAccount,
		ReasonFullInbox,
		ReasonCatchAll,
		ReasonTemporaryRejection,
		ReasonRateLimited,
		ReasonProviderBlocked,
		ReasonDNSError,
		ReasonSMTPTimeout,
		ReasonConnectionRefused,
		ReasonSMTPInconclusive,
		ReasonSMTPDisabled,
	}
}

// Metadata returns the metadata for a reason code and whether it exists.
func Metadata(rc ReasonCode) (Meta, bool) {
	m, ok := reasonMeta[rc]
	return m, ok
}
