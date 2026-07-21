package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
)

// countingHandlerService counts Verify calls and returns a canned valid result.
type countingHandlerService struct{ calls int }

func (s *countingHandlerService) Verify(_ context.Context, email string) verification.Assessment {
	s.calls++
	a := acceptedAssessment()
	a.Email = email
	return a
}

// newIdempotentServer builds the router with a memory-backed idempotency store.
func newIdempotentServer(t *testing.T, svc VerificationService) http.Handler {
	t.Helper()
	idem := store.NewMemory[verification.Assessment](100)
	return newRouter(newHandlers(svc, 4096, defaultTestBatch, idem, nil, 0), "")
}

// postIdem issues a POST /v1/verifications with an optional Idempotency-Key.
func postIdem(t *testing.T, h http.Handler, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/verifications", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestIdempotency_RepeatedKeyServedFromStore(t *testing.T) {
	svc := &countingHandlerService{}
	h := newIdempotentServer(t, svc)

	first := postIdem(t, h, `{"email":"a@example.com"}`, "key-123")
	second := postIdem(t, h, `{"email":"a@example.com"}`, "key-123")

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, first.Body.String(), second.Body.String(), "same key must return the same body")
	assert.Equal(t, 1, svc.calls, "the second keyed request must be served from the idempotency store")
}

func TestIdempotency_NoKeyAlwaysVerifies(t *testing.T) {
	svc := &countingHandlerService{}
	h := newIdempotentServer(t, svc)

	postIdem(t, h, `{"email":"a@example.com"}`, "")
	postIdem(t, h, `{"email":"a@example.com"}`, "")
	assert.Equal(t, 2, svc.calls, "requests without an Idempotency-Key must each verify")
}

func TestIdempotency_DifferentKeysVerifySeparately(t *testing.T) {
	svc := &countingHandlerService{}
	h := newIdempotentServer(t, svc)

	postIdem(t, h, `{"email":"a@example.com"}`, "key-A")
	postIdem(t, h, `{"email":"a@example.com"}`, "key-B")
	assert.Equal(t, 2, svc.calls, "distinct keys are distinct requests")
}

func TestIdempotency_MalformedKeyIgnored(t *testing.T) {
	svc := &countingHandlerService{}
	h := newIdempotentServer(t, svc)

	// A control-character key is not a usable idempotency key, so both verify.
	postIdem(t, h, `{"email":"a@example.com"}`, "bad\x01key")
	postIdem(t, h, `{"email":"a@example.com"}`, "bad\x01key")
	assert.Equal(t, 2, svc.calls)
}

func TestIdempotency_DisabledWhenNoStore(t *testing.T) {
	svc := &countingHandlerService{}
	// Default helper passes a nil idempotency store -> feature disabled.
	h := newTestServer(t, svc, 0)

	postIdem(t, h, `{"email":"a@example.com"}`, "key-123")
	postIdem(t, h, `{"email":"a@example.com"}`, "key-123")
	assert.Equal(t, 2, svc.calls, "with no idempotency store, every request verifies")
}
