package main

import (
	"net/http"
	"testing"

	"github.com/AfterShip/email-verifier/internal/ratelimit"
	"github.com/stretchr/testify/assert"
)

func newRateLimitedServer(t *testing.T, perMinute int) http.Handler {
	t.Helper()
	return newRouter(newHandlers(handlerOpts{
		svc:         okService(),
		batch:       defaultTestBatch,
		logger:      testLogger,
		rateLimiter: ratelimit.New(perMinute),
	}), "")
}

func TestRateLimit_BlocksOverLimit(t *testing.T) {
	h := newRateLimitedServer(t, 2) // burst 2

	body := `{"email":"a@example.com"}`
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodPost, "/v1/verifications", "application/json", body).Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodPost, "/v1/verifications", "application/json", body).Code)

	rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", body)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assertErrorEnvelope(t, rec, "rate_limited")
}

func TestRateLimit_ExemptsHealthAndReady(t *testing.T) {
	h := newRateLimitedServer(t, 1)
	// Exhaust the limit on a real route.
	do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"a@example.com"}`)
	assert.Equal(t, http.StatusTooManyRequests,
		do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"a@example.com"}`).Code)

	// Health and readiness must remain available regardless.
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/health", "", "").Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/ready", "", "").Code)
}

func TestRateLimit_DisabledWhenNil(t *testing.T) {
	h := newTestServer(t, okService(), 0) // no limiter
	for i := 0; i < 20; i++ {
		assert.Equal(t, http.StatusOK,
			do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"a@example.com"}`).Code)
	}
}
