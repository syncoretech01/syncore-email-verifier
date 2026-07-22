package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func eraserFor(s store.Store[verification.Assessment]) func(context.Context, string) error {
	return func(ctx context.Context, email string) error {
		return s.Delete(ctx, strings.ToLower(strings.TrimSpace(email)))
	}
}

func TestErasure_RemovesCachedEntry(t *testing.T) {
	cache := store.NewMemory[verification.Assessment](100)
	require.NoError(t, cache.Set(context.Background(), "person@example.com", acceptedAssessment(), time.Hour))

	h := newRouter(newHandlers(handlerOpts{
		svc: okService(), batch: defaultTestBatch, logger: testLogger, erase: eraserFor(cache),
	}), "")

	rec := do(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":"Person@Example.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	_, ok, err := cache.Get(context.Background(), "person@example.com")
	require.NoError(t, err)
	assert.False(t, ok, "the cached entry must be erased")
}

func TestErasure_DisabledWhenNoEraser(t *testing.T) {
	h := newTestServer(t, okService(), 0) // no erase func
	rec := do(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":"a@example.com"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assertErrorEnvelope(t, rec, "not_available")
}

func TestErasure_Validation(t *testing.T) {
	cache := store.NewMemory[verification.Assessment](10)
	h := newRouter(newHandlers(handlerOpts{svc: okService(), batch: defaultTestBatch, logger: testLogger, erase: eraserFor(cache)}), "")

	assert.Equal(t, http.StatusUnsupportedMediaType,
		do(t, h, http.MethodPost, "/admin/erasure", "text/plain", `{"email":"a@x.com"}`).Code)
	assert.Equal(t, http.StatusBadRequest,
		do(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":""}`).Code)
	assert.Equal(t, http.StatusBadRequest,
		do(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":"a@x.com"}{}`).Code)
}

func TestErasure_RequiresAuth(t *testing.T) {
	cache := store.NewMemory[verification.Assessment](10)
	h := newRouter(newHandlers(handlerOpts{svc: okService(), batch: defaultTestBatch, logger: testLogger, erase: eraserFor(cache)}), testAuthToken)

	assert.Equal(t, http.StatusUnauthorized,
		do(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":"a@x.com"}`).Code)
	assert.Equal(t, http.StatusOK,
		doAuth(t, h, http.MethodPost, "/admin/erasure", "application/json", `{"email":"a@x.com"}`, "Bearer "+testAuthToken).Code)
}
