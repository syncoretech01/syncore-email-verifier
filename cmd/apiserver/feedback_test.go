package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AfterShip/email-verifier/internal/feedback"
	"github.com/AfterShip/email-verifier/internal/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFeedbackServer(t *testing.T, key string) (http.Handler, *feedback.Store) {
	t.Helper()
	fs := feedback.New()
	h := newRouter(newHandlers(handlerOpts{
		svc: okService(), batch: defaultTestBatch, logger: testLogger,
		feedbackStore: fs, feedbackKey: []byte(key),
	}), "")
	return h, fs
}

func postFeedback(t *testing.T, h http.Handler, body, signature string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/feedback", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if signature != "" {
		r.Header.Set(jobs.SignatureHeader, signature)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestFeedback_SignedEventRecorded(t *testing.T) {
	h, fs := newFeedbackServer(t, "feedback-key")
	body := `{"email":"user@bounced.com","type":"bounced"}`
	sig := jobs.Sign([]byte("feedback-key"), []byte(body))

	rec := postFeedback(t, h, body, sig)
	require.Equal(t, http.StatusAccepted, rec.Code)

	rep, ok := fs.Domain("bounced.com")
	require.True(t, ok)
	assert.Equal(t, 1, rep.Bounced)
}

func TestFeedback_BadSignatureRejected(t *testing.T) {
	h, fs := newFeedbackServer(t, "feedback-key")
	rec := postFeedback(t, h, `{"email":"a@x.com","type":"bounced"}`, "sha256=deadbeef")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorEnvelope(t, rec, "unauthorized")
	_, ok := fs.Domain("x.com")
	assert.False(t, ok, "a rejected event must not be recorded")
}

func TestFeedback_MissingSignatureRejected(t *testing.T) {
	h, _ := newFeedbackServer(t, "feedback-key")
	rec := postFeedback(t, h, `{"email":"a@x.com","type":"bounced"}`, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestFeedback_ValidationAfterSignature(t *testing.T) {
	h, _ := newFeedbackServer(t, "feedback-key")
	body := `{"email":"","type":""}`
	rec := postFeedback(t, h, body, jobs.Sign([]byte("feedback-key"), []byte(body)))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFeedback_DisabledWhenNoKey(t *testing.T) {
	h := newTestServer(t, okService(), 0) // no feedback store/key
	rec := postFeedback(t, h, `{"email":"a@x.com","type":"bounced"}`, "sha256=x")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assertErrorEnvelope(t, rec, "not_available")
}
