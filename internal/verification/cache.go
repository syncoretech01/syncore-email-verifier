package verification

import (
	"context"
	"strings"
	"time"

	"github.com/AfterShip/email-verifier/internal/store"
)

// verifier is the behavior CachingVerifier wraps — the same method set the HTTP
// handler depends on. *Service satisfies it.
type verifier interface {
	Verify(ctx context.Context, rawEmail string) Assessment
}

// CachingVerifier memoizes Assessments in a TTL store so repeat checks of the
// same address do not repeat DNS/SMTP work. Terminal results (valid/invalid, and
// non-retryable risky) are cached for the full TTL; retryable results
// (unknown, and full_inbox) use a shorter TTL so a transient failure is retried
// soon. It never alters classification — only whether the work is repeated.
type CachingVerifier struct {
	next       verifier
	cache      store.Store[Assessment]
	ttl        time.Duration
	unknownTTL time.Duration
}

// NewCachingVerifier wraps next with a TTL cache. ttl is used for terminal
// (non-retryable) results; unknownTTL for retryable results. unknownTTL is
// clamped to at most ttl, and when non-positive defaults to min(ttl, 1m).
func NewCachingVerifier(next verifier, cache store.Store[Assessment], ttl, unknownTTL time.Duration) *CachingVerifier {
	if unknownTTL <= 0 {
		unknownTTL = ttl
		if unknownTTL > time.Minute {
			unknownTTL = time.Minute
		}
	}
	if unknownTTL > ttl {
		unknownTTL = ttl
	}
	return &CachingVerifier{
		next:       next,
		cache:      cache,
		ttl:        ttl,
		unknownTTL: unknownTTL,
	}
}

// Verify returns a cached Assessment when one is live, otherwise runs the wrapped
// verifier and caches the result under a normalized key.
func (c *CachingVerifier) Verify(ctx context.Context, rawEmail string) Assessment {
	key := normalizeCacheKey(rawEmail)
	if key != "" {
		if a, ok := c.cache.Get(key); ok {
			return a
		}
	}

	a := c.next.Verify(ctx, rawEmail)

	if key != "" {
		c.cache.Set(key, a, c.ttlFor(a))
	}
	return a
}

// ttlFor selects the TTL for an assessment: the shorter unknownTTL for retryable
// results (transient failures worth retrying soon), the full ttl otherwise.
func (c *CachingVerifier) ttlFor(a Assessment) time.Duration {
	if a.Retryable {
		return c.unknownTTL
	}
	return c.ttl
}

// normalizeCacheKey lowercases and trims the address so that, e.g.,
// "Person@Example.com" and "person@example.com" share one cache entry. It returns
// "" for empty input, which is never cached.
func normalizeCacheKey(rawEmail string) string {
	return strings.ToLower(strings.TrimSpace(rawEmail))
}
