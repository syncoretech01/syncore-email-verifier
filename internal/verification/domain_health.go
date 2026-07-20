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

func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}
