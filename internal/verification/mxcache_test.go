package verification

import (
	"context"
	"net"
	"testing"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/stretchr/testify/assert"
)

func fixedClock() Option { return WithClock(func() time.Time { return fixedTime }) }

func TestMXCache_HitAvoidsSecondLookup(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
	cache := store.NewMemory[MXCacheEntry](100)
	svc := NewService(e, fixedClock(), WithMXCache(cache, time.Minute))

	// Two different addresses at the same domain (the stub always returns
	// example.com) should share a single MX lookup.
	a1 := svc.Verify(context.Background(), "a@example.com")
	a2 := svc.Verify(context.Background(), "b@example.com")

	assert.Equal(t, 1, e.mxCalls, "second same-domain verification must hit the MX cache")
	// The cached MX evidence is applied on the hit.
	assert.True(t, a1.Domain.HasMXRecords)
	assert.True(t, a2.Domain.HasMXRecords)
	assert.Equal(t, "mx", a2.Domain.MailHostSource)
}

func TestMXCache_FailedResolutionNotCached(t *testing.T) {
	// A DNS timeout must not be cached: every call re-checks so a recovered
	// domain isn't stuck as unknown.
	e := &stubEngine{syntax: validSyntax(), mx: &emailverifier.Mx{MailHostSource: "none"}, mxErr: &net.DNSError{IsTimeout: true}}
	cache := store.NewMemory[MXCacheEntry](100)
	svc := NewService(e, fixedClock(), WithMXCache(cache, time.Minute))

	svc.Verify(context.Background(), "a@example.com")
	svc.Verify(context.Background(), "b@example.com")
	assert.Equal(t, 2, e.mxCalls, "transient DNS failures must not be cached")
}

func TestMXCache_DisabledByDefault(t *testing.T) {
	e := &stubEngine{syntax: validSyntax(), mx: mxResolved(), smtp: acceptedSMTP()}
	svc := NewService(e, fixedClock()) // no WithMXCache
	svc.Verify(context.Background(), "a@example.com")
	svc.Verify(context.Background(), "b@example.com")
	assert.Equal(t, 2, e.mxCalls, "without a cache, every verification resolves MX")
}
