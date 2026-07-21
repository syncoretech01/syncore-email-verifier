// Package verification orchestrates the Phase 1A engine's granular methods into a
// single, classified Assessment. It performs no HTTP, configuration loading, or
// persistence — those belong to later phases.
package verification

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/classify"
)

// Engine is the subset of *emailverifier.Verifier the service depends on. Using
// the granular methods (rather than the monolithic Verify) lets the service
// interpret each stage's evidence and errors, and makes it stub-testable.
type Engine interface {
	ParseAddress(email string) emailverifier.Syntax
	CheckMX(domain string) (*emailverifier.Mx, error)
	CheckSMTP(domain, username string) (*emailverifier.SMTP, error)
	IsDisposable(domain string) bool
	IsRoleAccount(username string) bool
	IsFreeDomain(domain string) bool
	SuggestDomain(domain string) string
}

// ErrorInfo is sanitized diagnostic information. It never contains raw SMTP
// server text, proxy URIs/credentials, IP addresses, or LookupError details.
type ErrorInfo struct {
	Code    string `json:"code"`    // "input" | "dns" | "mx" | "smtp"
	Message string `json:"message"` // safe, human-readable, static per reason
}

// DomainEvidence is neutral (non-HTTP) structured domain evidence.
type DomainEvidence struct {
	HasMXRecords   bool   `json:"has_mx_records"`
	NullMX         bool   `json:"null_mx"`
	ImplicitMX     bool   `json:"implicit_mx"`
	MailHostSource string `json:"mail_host_source"`
	Disposable     bool   `json:"disposable"`
	FreeProvider   bool   `json:"free_provider"`
	Suggestion     string `json:"suggestion"`
	// Health is populated only when domain-health checks are enabled and the
	// domain resolved; nil otherwise.
	Health *DomainHealthEvidence `json:"health,omitempty"`
}

// DomainHealthEvidence reports free domain-hygiene signals derived from DNS.
// DKIM is intentionally omitted: it is selector-specific and cannot be verified
// without a signed message. These signals do not affect classification.
type DomainHealthEvidence struct {
	SPF   bool `json:"spf"`   // a v=spf1 TXT record is published
	DMARC bool `json:"dmarc"` // a v=DMARC1 policy is published at _dmarc.<domain>
	MX    bool `json:"mx"`    // the domain has a usable mail host
}

// AccountEvidence is neutral structured account evidence.
type AccountEvidence struct {
	RoleAccount bool `json:"role_account"`
}

// Assessment is the single internal verification result. It carries the
// structured evidence for a later POST presenter plus the legacy
// emailverifier.Result for a later GET presenter. It contains no HTTP behavior.
type Assessment struct {
	Email      string
	Status     classify.Status
	ReasonCode classify.ReasonCode
	Retryable  bool
	Confidence int
	// DeliverabilityScore (0-100) estimates how likely the address is to accept
	// mail. It is distinct from Confidence, which is our certainty in the
	// classification. Deterministic; derived from status + evidence, no network.
	DeliverabilityScore int
	// ScoreComponents decomposes DeliverabilityScore into sub-signals.
	ScoreComponents ScoreComponents
	CheckedAt       time.Time
	Source          string

	Syntax  emailverifier.Syntax
	Domain  DomainEvidence
	Account AccountEvidence
	SMTP    *emailverifier.SMTP

	SMTPAttempted   bool
	SMTPCheckReason classify.SMTPCheckReason

	// Suppressed is true when the address is on the do-not-verify list; when set,
	// no network check was performed.
	Suppressed bool

	Error *ErrorInfo

	// Result is the legacy-compatible evidence for the later GET presenter.
	Result *emailverifier.Result
}

// Service converts engine evidence into an Assessment.
type Service struct {
	engine       Engine
	clock        func() time.Time
	smtpEnabled  bool
	domainHealth bool
	lookupTXT    func(name string) ([]string, error)
	suppressed   func(email string) bool
}

// Option configures a Service.
type Option func(*Service)

// WithClock injects a clock for deterministic CheckedAt values.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithSMTPEnabled sets whether SMTP recipient checks are performed.
func WithSMTPEnabled(enabled bool) Option {
	return func(s *Service) { s.smtpEnabled = enabled }
}

// WithDomainHealth enables free SPF/DMARC/MX domain-health lookups, folded into
// the domain evidence. Off by default (adds DNS TXT lookups per verification).
func WithDomainHealth(enabled bool) Option {
	return func(s *Service) { s.domainHealth = enabled }
}

// WithTXTLookup injects the DNS TXT resolver used for domain health, enabling
// deterministic tests. Defaults to net.LookupTXT.
func WithTXTLookup(fn func(name string) ([]string, error)) Option {
	return func(s *Service) {
		if fn != nil {
			s.lookupTXT = fn
		}
	}
}

// WithSuppressionCheck injects a predicate that reports whether an address is on
// the do-not-verify list. Suppressed addresses skip all network checks.
func WithSuppressionCheck(fn func(email string) bool) Option {
	return func(s *Service) { s.suppressed = fn }
}

// NewService builds a Service. By default SMTP checks are enabled, CheckedAt
// uses the wall clock in UTC, and domain-health checks are off.
func NewService(engine Engine, opts ...Option) *Service {
	s := &Service{
		engine:      engine,
		clock:       func() time.Time { return time.Now().UTC() },
		smtpEnabled: true,
		lookupTXT:   net.LookupTXT,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Verify runs the full verification pipeline and returns an Assessment.
//
// The ctx is accepted for request-scoped values and forward compatibility; in
// Phase 1 it does NOT cancel the underlying DNS or SMTP work — the engine has no
// cancellation support, and the only real bound is its own dial/operation
// deadlines.
func (s *Service) Verify(ctx context.Context, rawEmail string) Assessment {
	_ = ctx // not used for cancellation in Phase 1

	email := strings.TrimSpace(rawEmail)

	// Suppression is honored before any network check.
	if s.suppressed != nil && s.suppressed(email) {
		return s.suppressedAssessment(email)
	}

	syntax := s.engine.ParseAddress(email)
	ev := classify.Evidence{Email: email, SyntaxValid: syntax.Valid}

	if !syntax.Valid {
		return s.finalize(email, syntax, ev, nil, nil)
	}

	// Local, network-free evidence.
	ev.Free = s.engine.IsFreeDomain(syntax.Domain)
	ev.RoleAccount = s.engine.IsRoleAccount(syntax.Username)
	ev.Disposable = s.engine.IsDisposable(syntax.Domain)
	ev.Suggestion = s.engine.SuggestDomain(syntax.Domain)

	// DNS / MX resolution.
	mx, mxErr := s.engine.CheckMX(syntax.Domain)
	if mx != nil {
		ev.HasMXRecords = mx.HasMXRecord
		ev.NullMX = mx.NullMX
		ev.MailHostSource = classify.MailHostSource(mx.MailHostSource)
	}
	if mxErr != nil {
		ev.DNS = classifyDNSError(mxErr)
	} else {
		ev.DNS = classify.DNSResolved
	}

	// Decide whether to run an SMTP recipient check. Short-circuits set an
	// explicit SMTPCheckReason (never inferred later from recipient_result).
	var smtpEv *emailverifier.SMTP
	switch {
	case ev.DNS != classify.DNSResolved:
		ev.SMTPCheckReason = classify.CheckDNSFailure
	case ev.NullMX:
		ev.SMTPCheckReason = classify.CheckNullMX
	case ev.MailHostSource == classify.MailHostNone:
		ev.SMTPCheckReason = classify.CheckNoMailHost
	case ev.Disposable:
		ev.SMTPCheckReason = classify.CheckDisposable
	case !s.smtpEnabled:
		ev.SMTPCheckReason = classify.CheckDisabled
	default:
		smtpEv = s.runSMTP(syntax, &ev)
	}

	a := s.finalize(email, syntax, ev, mx, smtpEv)
	// Domain health is optional, evidence-only, and never changes the status. It
	// runs only when enabled and the domain actually resolved.
	if s.domainHealth && ev.DNS == classify.DNSResolved {
		a.Domain.Health = s.checkDomainHealth(syntax.Domain, ev)
	}
	return a
}

// suppressedAssessment returns a network-free result for a do-not-verify address.
// It is marked risky (do not send) with the Suppressed flag set; no MX/SMTP is
// performed. Syntax is still parsed (network-free) for a structured response.
func (s *Service) suppressedAssessment(email string) Assessment {
	syntax := s.engine.ParseAddress(email)
	return Assessment{
		Email:      email,
		Status:     classify.StatusRisky,
		Suppressed: true,
		CheckedAt:  s.clock(),
		Syntax:     syntax,
		Error:      &ErrorInfo{Code: "policy", Message: "address is on the suppression list"},
		Result: &emailverifier.Result{
			Email:     email,
			Reachable: "unknown",
			Syntax:    syntax,
		},
	}
}

// runSMTP performs the recipient check and folds its evidence into ev, preserving
// partial evidence even when CheckSMTP returns both a result and an error.
func (s *Service) runSMTP(syntax emailverifier.Syntax, ev *classify.Evidence) *emailverifier.SMTP {
	smtpEv, smtpErr := s.engine.CheckSMTP(syntax.Domain, syntax.Username)
	ev.SMTPAttempted = true

	if smtpEv != nil {
		ev.RecipientResult = classify.RecipientResult(smtpEv.RecipientResult)
		ev.RecipientReason = classify.RecipientReason(smtpEv.RecipientReason)
		ev.CatchAllResult = classify.CatchAllResult(smtpEv.CatchAllResult)
		ev.SMTPCode = smtpEv.SMTPCode
		ev.Source = classify.Source(smtpEv.Source)
	}
	ev.Transport = classifyTransport(smtpErr)

	switch {
	case ev.Source == classify.SourceAPI:
		ev.SMTPCheckReason = classify.CheckAPIVerifier
	case ev.CatchAllResult == classify.CatchAllConfirmed:
		ev.SMTPCheckReason = classify.CheckCatchAll
	default:
		ev.SMTPCheckReason = classify.CheckAttempted
	}
	return smtpEv
}

// finalize classifies the evidence and assembles the Assessment (including the
// legacy Result and sanitized error).
func (s *Service) finalize(email string, syntax emailverifier.Syntax, ev classify.Evidence, mx *emailverifier.Mx, smtpEv *emailverifier.SMTP) Assessment {
	c := classify.Classify(ev)

	a := Assessment{
		Email:               email,
		Status:              c.Status,
		ReasonCode:          c.ReasonCode,
		Retryable:           c.Retryable,
		Confidence:          c.Confidence,
		DeliverabilityScore: deliverabilityScore(c.Status, c.Confidence, ev),
		ScoreComponents:     computeScoreComponents(ev),
		CheckedAt:           s.clock(),
		Source:              string(ev.Source),
		Syntax:              syntax,
		Account:             AccountEvidence{RoleAccount: ev.RoleAccount},
		SMTP:                smtpEv,
		SMTPAttempted:       ev.SMTPAttempted,
		SMTPCheckReason:     ev.SMTPCheckReason,
		Error:               sanitizedError(c.ReasonCode),
		Domain: DomainEvidence{
			HasMXRecords:   ev.HasMXRecords,
			NullMX:         ev.NullMX,
			ImplicitMX:     mx != nil && mx.ImplicitMX,
			MailHostSource: string(ev.MailHostSource),
			Disposable:     ev.Disposable,
			FreeProvider:   ev.Free,
			Suggestion:     ev.Suggestion,
		},
	}
	a.Result = buildLegacyResult(email, syntax, ev, mx, smtpEv, c.Status)
	return a
}

// buildLegacyResult assembles the emailverifier.Result the later GET presenter
// needs, preserving legacy fields, legacy reachable semantics, and the additive
// Phase 1A evidence. Gravatar is intentionally left nil in Phase 1B.
func buildLegacyResult(email string, syntax emailverifier.Syntax, ev classify.Evidence, mx *emailverifier.Mx, smtpEv *emailverifier.SMTP, status classify.Status) *emailverifier.Result {
	res := &emailverifier.Result{
		Email:        email,
		Reachable:    reachableFromStatus(status),
		Syntax:       syntax,
		SMTP:         smtpEv,
		Gravatar:     nil,
		Suggestion:   ev.Suggestion,
		Disposable:   ev.Disposable,
		RoleAccount:  ev.RoleAccount,
		Free:         ev.Free,
		HasMxRecords: ev.HasMXRecords,
	}
	if mx != nil {
		res.NullMX = mx.NullMX
		res.MailHostSource = mx.MailHostSource
	}
	return res
}

// reachableFromStatus preserves the legacy Reachable enum.
func reachableFromStatus(status classify.Status) string {
	switch status {
	case classify.StatusValid:
		return "yes"
	case classify.StatusInvalid:
		return "no"
	default: // risky, unknown
		return "unknown"
	}
}

// classifyDNSError conservatively normalizes a CheckMX error. NXDOMAIN becomes
// not_found; timeouts, temporary failures, and any ambiguous DNS failure become
// a temporary error (never not_found).
func classifyDNSError(err error) classify.DNSOutcome {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return classify.DNSNotFound
		}
		return classify.DNSTempError
	}
	var le *emailverifier.LookupError
	if errors.As(err, &le) {
		if le.Message == emailverifier.ErrNoSuchHost {
			return classify.DNSNotFound
		}
		return classify.DNSTempError
	}
	// Ambiguous DNS failure → temporary, not a hard "not found".
	return classify.DNSTempError
}

// classifyTransport normalizes a CheckSMTP error into a transport signal using
// the engine's normalized LookupError messages (not arbitrary strings).
func classifyTransport(err error) classify.Transport {
	if err == nil {
		return classify.TransportNone
	}
	var le *emailverifier.LookupError
	if errors.As(err, &le) {
		switch le.Message {
		case emailverifier.ErrTimeout:
			return classify.TransportTimeout
		case emailverifier.ErrConnRefused:
			return classify.TransportRefused
		}
	}
	return classify.TransportOther
}

// errorInfoByReason maps invalid/unknown outcomes to a safe, static diagnostic.
// Valid and risky outcomes have no error (nil).
var errorInfoByReason = map[classify.ReasonCode]ErrorInfo{
	classify.ReasonSyntaxInvalid:      {Code: "input", Message: "email address has invalid syntax"},
	classify.ReasonDomainNotFound:     {Code: "dns", Message: "domain does not exist"},
	classify.ReasonDNSError:           {Code: "dns", Message: "temporary DNS resolution failure"},
	classify.ReasonNullMX:             {Code: "mx", Message: "domain does not accept email (null MX)"},
	classify.ReasonNoMailHost:         {Code: "mx", Message: "domain has no usable mail server"},
	classify.ReasonMailboxRejected:    {Code: "smtp", Message: "recipient mailbox does not exist"},
	classify.ReasonSMTPTimeout:        {Code: "smtp", Message: "connection to the mail server timed out"},
	classify.ReasonConnectionRefused:  {Code: "smtp", Message: "the mail server refused the connection"},
	classify.ReasonTemporaryRejection: {Code: "smtp", Message: "the mail server temporarily rejected the recipient"},
	classify.ReasonRateLimited:        {Code: "smtp", Message: "the mail server rate-limited the request"},
	classify.ReasonProviderBlocked:    {Code: "smtp", Message: "the mail server blocked the request"},
	classify.ReasonSMTPInconclusive:   {Code: "smtp", Message: "the mail server returned an inconclusive result"},
	classify.ReasonSMTPDisabled:       {Code: "smtp", Message: "smtp verification is disabled"},
}

func sanitizedError(rc classify.ReasonCode) *ErrorInfo {
	if e, ok := errorInfoByReason[rc]; ok {
		return &ErrorInfo{Code: e.Code, Message: e.Message}
	}
	return nil
}
