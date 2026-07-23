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

// dnsblBlockedScore is the deliverability-score ceiling for a domain found on a
// domain blocklist (DNSBL). A listed domain is a strong "do not send" signal.
const dnsblBlockedScore = 15

// applyBlocklistPenalty caps the score when the domain is blocklisted. It never
// raises the score and never touches the classification.
func applyBlocklistPenalty(score int, blocklisted bool) int {
	if blocklisted && score > dnsblBlockedScore {
		return dnsblBlockedScore
	}
	return clampScore(score)
}

// Catch-all sub-confidence: a confirmed catch-all domain accepts every recipient,
// so per-mailbox verification is impossible. But the domain's real sending
// history (from the feedback loop) tells us whether it tends to accept mail that
// sticks. These labels surface that nuance without changing the classification.
const (
	CatchAllLikelyValid   = "likely_valid"
	CatchAllLikelyInvalid = "likely_invalid"
	CatchAllUnknown       = "unknown"
)

const (
	// catchAllMinSamples is the minimum delivered+bounced history before the
	// bounce rate is trusted enough to label a catch-all.
	catchAllMinSamples = 5
	// catchAllLikelyValidScore is the deliverability score a reliably-delivering
	// catch-all is lifted to (above the flat catch-all baseline of 50).
	catchAllLikelyValidScore = 75
)

// refineCatchAllScore uses a confirmed catch-all domain's real bounce history to
// derive a sub-confidence label and, for a reliably-delivering domain, lift the
// deliverability score above the flat catch-all baseline. It never raises the
// score for a poor-history domain (adjustScoreForReputation caps that separately)
// and never touches the classification.
func refineCatchAllScore(score int, rep DomainReputationEvidence) (int, string) {
	if rep.Delivered+rep.Bounced < catchAllMinSamples {
		return score, CatchAllUnknown // not enough history to judge
	}
	switch {
	case rep.BounceRate < 0.1:
		// A catch-all that reliably delivers is likely accepting real mail.
		if score < catchAllLikelyValidScore {
			score = catchAllLikelyValidScore
		}
		return clampScore(score), CatchAllLikelyValid
	case rep.BounceRate >= 0.2:
		return score, CatchAllLikelyInvalid // the reputation cap lowers the score
	default:
		return score, CatchAllUnknown
	}
}

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
