package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/AfterShip/email-verifier/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newObservableServer(t *testing.T, svc VerificationService, ready func(context.Context) error) (http.Handler, *metrics.Registry) {
	t.Helper()
	reg := metrics.New()
	h := newRouter(newHandlers(handlerOpts{
		svc:     svc,
		batch:   defaultTestBatch,
		metrics: reg,
		logger:  testLogger,
		ready:   ready,
	}), "")
	return h, reg
}

func TestObservability_MetricsRecorded(t *testing.T) {
	h, _ := newObservableServer(t, okService(), nil)

	// Drive a couple of verifications + a health check.
	do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"a@example.com"}`)
	do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"b@example.com"}`)
	do(t, h, http.MethodGet, "/health", "", "")

	rec := do(t, h, http.MethodGet, "/metrics", "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, `verifications_total{status="valid"} 2`)
	assert.Contains(t, body, "http_requests_total")
	assert.Contains(t, body, "http_request_duration_seconds_count")
	// The metrics response carries a request ID header from the observe middleware.
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestObservability_MetricsDoNotLeakEmail(t *testing.T) {
	h, _ := newObservableServer(t, okService(), nil)
	// The legacy GET path contains an email; the route label must not.
	do(t, h, http.MethodGet, "/v1/person@example.com/verification", "", "")
	rec := do(t, h, http.MethodGet, "/metrics", "", "")
	body := rec.Body.String()
	assert.Contains(t, body, `route="/v1/:email/verification"`)
	assert.NotContains(t, body, "person@example.com")
}

func TestReady_OKAndNotReady(t *testing.T) {
	// Ready.
	h, _ := newObservableServer(t, okService(), func(context.Context) error { return nil })
	rec := do(t, h, http.MethodGet, "/ready", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	// Not ready (store probe fails).
	h2, _ := newObservableServer(t, okService(), func(context.Context) error { return errors.New("db down") })
	rec2 := do(t, h2, http.MethodGet, "/ready", "", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec2.Code)
}

func TestReady_OpenWithAuthEnabled(t *testing.T) {
	reg := metrics.New()
	h := newRouter(newHandlers(handlerOpts{svc: okService(), batch: defaultTestBatch, metrics: reg, logger: testLogger}), testAuthToken)
	// /ready and /health stay open; /metrics requires the token.
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/ready", "", "").Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/health", "", "").Code)
	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/metrics", "", "").Code)
}
