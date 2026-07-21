package main

import (
	"net/http"
	"testing"

	"github.com/AfterShip/email-verifier/internal/quota"
	"github.com/stretchr/testify/assert"
)

func newQuotaServer(t *testing.T, daily int) http.Handler {
	t.Helper()
	return newRouter(newHandlers(handlerOpts{
		svc: okService(), batch: defaultTestBatch, logger: testLogger, quota: quota.New(daily),
	}), "")
}

func TestQuota_BlocksOverDailyLimit(t *testing.T) {
	h := newQuotaServer(t, 2)
	body := `{"email":"a@example.com"}`
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodPost, "/v1/verifications", "application/json", body).Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodPost, "/v1/verifications", "application/json", body).Code)

	rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", body)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assertErrorEnvelope(t, rec, "quota_exceeded")
}

func TestQuota_ExemptsHealthAndReady(t *testing.T) {
	h := newQuotaServer(t, 1)
	do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"a@example.com"}`)
	// Quota exhausted for real routes, but probes stay available.
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/health", "", "").Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/ready", "", "").Code)
}
