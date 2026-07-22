package verification

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func repOption(rep DomainReputationEvidence) Option {
	return WithReputation(func(string) (DomainReputationEvidence, bool) { return rep, true })
}

func TestCatchAll_LikelyValid_LiftsScore(t *testing.T) {
	// A confirmed catch-all with a strong delivery history is labeled likely_valid
	// and lifted above the flat catch-all baseline — but stays classified risky.
	rep := DomainReputationEvidence{Delivered: 98, Bounced: 2, BounceRate: 0.02}
	a := run(t, riskyCatchAllEngine(), repOption(rep))

	assert.Equal(t, "risky", string(a.Status))
	assert.Equal(t, "catch_all", string(a.ReasonCode))
	assert.Equal(t, CatchAllLikelyValid, a.CatchAllLikelihood)
	assert.Equal(t, catchAllLikelyValidScore, a.DeliverabilityScore)
}

func TestCatchAll_LikelyInvalid_CapsScore(t *testing.T) {
	rep := DomainReputationEvidence{Delivered: 10, Bounced: 90, BounceRate: 0.9}
	a := run(t, riskyCatchAllEngine(), repOption(rep))

	assert.Equal(t, CatchAllLikelyInvalid, a.CatchAllLikelihood)
	assert.LessOrEqual(t, a.DeliverabilityScore, 20)
	assert.Equal(t, "risky", string(a.Status)) // classification unchanged
}

func TestCatchAll_InsufficientHistory_Unknown(t *testing.T) {
	rep := DomainReputationEvidence{Delivered: 2, Bounced: 0, BounceRate: 0}
	a := run(t, riskyCatchAllEngine(), repOption(rep))
	assert.Equal(t, CatchAllUnknown, a.CatchAllLikelihood)
}

func TestCatchAll_ModerateBounce_Unknown(t *testing.T) {
	// Between the thresholds (0.1 <= rate < 0.2): not confident either way.
	rep := DomainReputationEvidence{Delivered: 85, Bounced: 15, BounceRate: 0.15}
	a := run(t, riskyCatchAllEngine(), repOption(rep))
	assert.Equal(t, CatchAllUnknown, a.CatchAllLikelihood)
}

func TestCatchAll_NoReputation_DefaultsUnknown(t *testing.T) {
	a := run(t, riskyCatchAllEngine()) // no WithReputation wired
	assert.Equal(t, CatchAllUnknown, a.CatchAllLikelihood)
}

func TestCatchAll_NonCatchAll_NoLikelihood(t *testing.T) {
	// An accepted (non-catch-all) result carries no catch-all likelihood.
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
	a := run(t, e, repOption(DomainReputationEvidence{Delivered: 100, Bounced: 1, BounceRate: 0.01}))
	assert.Empty(t, a.CatchAllLikelihood)
	// Reputation is still attached and a good history does not cap a valid result.
	assert.NotNil(t, a.Domain.Reputation)
}
