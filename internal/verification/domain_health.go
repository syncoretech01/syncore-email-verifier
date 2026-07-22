package verification

import (
	"strings"

	"github.com/AfterShip/email-verifier/internal/classify"
)

// checkDomainHealth gathers free domain-hygiene signals from DNS. A failed TXT
// lookup is treated as "not published" (false), never an error surfaced to the
// caller — health is best-effort evidence.
func (s *Service) checkDomainHealth(domain string, ev classify.Evidence) *DomainHealthEvidence {
	h := &DomainHealthEvidence{MX: mxHealthy(ev.MailHostSource)}

	if txt, err := s.lookupTXT(domain); err == nil {
		h.SPF = hasRecordWithPrefix(txt, "v=spf1")
	}
	if txt, err := s.lookupTXT("_dmarc." + domain); err == nil {
		h.DMARC = hasRecordWithPrefix(txt, "v=dmarc1")
	}
	return h
}

// mxHealthy reports whether the domain resolved to a usable mail host (an
// explicit MX or an implicit A/AAAA), as opposed to null/none.
func mxHealthy(mailHostSource classify.MailHostSource) bool {
	switch mailHostSource {
	case classify.MailHostMX, classify.MailHostA, classify.MailHostAAAA:
		return true
	default:
		return false
	}
}

// hasRecordWithPrefix reports whether any TXT record begins with prefix
// (case-insensitively, ignoring surrounding whitespace).
func hasRecordWithPrefix(records []string, prefix string) bool {
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r)), prefix) {
			return true
		}
	}
	return false
}

// deliverabilityScore estimates (0-100) how likely an address is to accept mail.
// It is deterministic and network-free, derived from the classification and
// evidence. This is a v1 heuristic; per-domain reputation priors (the feedback
// loop) can refine it later without changing this contract.
//
//   - invalid           -> 0   (will not accept mail)
//   - unknown           -> Confidence (low; reflects retryable uncertainty)
//   - valid             -> 95, less a role-account penalty
//   - risky             -> Confidence, capped hard for disposable/catch-all and
//     reduced for role accounts
func deliverabilityScore(status classify.Status, confidence int, ev classify.Evidence) int {
	switch status {
	case classify.StatusInvalid:
		return 0
	case classify.StatusUnknown:
		return clampScore(confidence)
	}

	score := confidence
	if status == classify.StatusValid {
		score = 95
	}
	if ev.Disposable && score > 15 {
		score = 15
	}
	if ev.CatchAllResult == classify.CatchAllConfirmed && score > 50 {
		score = 50
	}
	if ev.RoleAccount {
		score -= 10
	}
	return clampScore(score)
}

// ScoreComponents decomposes the deliverability score into its deterministic
// sub-signals (0-100 each), so callers can see why an address scored as it did.
type ScoreComponents struct {
	Syntax  int `json:"syntax"`  // is the address well-formed
	Domain  int `json:"domain"`  // does the domain accept mail (MX/null-mx/disposable)
	Mailbox int `json:"mailbox"` // did the mailbox accept (SMTP recipient result)
}

// computeScoreComponents derives the sub-scores from evidence. Network-free.
func computeScoreComponents(ev classify.Evidence) ScoreComponents {
	c := ScoreComponents{}

	if ev.SyntaxValid {
		c.Syntax = 100
	}

	switch {
	case !ev.SyntaxValid:
		c.Domain = 0
	case ev.DNS != classify.DNSResolved:
		c.Domain = 30 // couldn't confirm the domain
	case ev.NullMX || ev.MailHostSource == classify.MailHostNone:
		c.Domain = 0 // domain refuses mail
	case ev.Disposable:
		c.Domain = 30
	default:
		c.Domain = 100
	}

	switch {
	case ev.RecipientResult == classify.RecipientAccepted:
		c.Mailbox = 100
	case ev.RecipientResult == classify.RecipientRejected:
		c.Mailbox = 0
	case ev.CatchAllResult == classify.CatchAllConfirmed:
		c.Mailbox = 50
	default:
		c.Mailbox = 40 // not checked / temporary / blocked / unknown
	}

	return c
}

// gravatarBonus is the small deliverability-score nudge a public Gravatar
// profile earns — a weak "this is a real, used address" engagement signal.
const gravatarBonus = 8

// applyGravatarBonus rewards a public Gravatar profile with a small, capped
// bonus, but only for uncertain results (unknown/risky). A valid result is
// already high and an invalid one refuses mail regardless of a public profile,
// so neither is changed. Never touches the classification.
func applyGravatarBonus(score int, status classify.Status) int {
	switch status {
	case classify.StatusUnknown, classify.StatusRisky:
		return clampScore(score + gravatarBonus)
	default:
		return score
	}
}

// adjustScoreForReputation lowers the score for a domain with a poor real-world
// bounce history. It never raises the score and never touches the classification.
func adjustScoreForReputation(score int, rep DomainReputationEvidence) int {
	switch {
	case rep.BounceRate >= 0.5:
		if score > 20 {
			score = 20
		}
	case rep.BounceRate >= 0.2:
		if score > 50 {
			score = 50
		}
	}
	return clampScore(score)
}

func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}
