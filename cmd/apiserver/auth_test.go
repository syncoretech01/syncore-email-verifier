package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testAuthToken = "s3cr3t-token-value"

// doAuth drives a request with an optional Authorization header. It mirrors the
// package-level do helper but adds header control for the auth cases.
func doAuth(t *testing.T, h http.Handler, method, target, contentType, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAuth_DisabledWhenNoToken(t *testing.T) {
	// With no configured token, auth is a pass-through: endpoints work headerless.
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuth_MissingTokenRejected(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorEnvelope(t, rec, "unauthorized")
}

func TestAuth_WrongTokenRejected(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, "Bearer wrong-token")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertErrorEnvelope(t, rec, "unauthorized")
}

func TestAuth_MalformedHeaderRejected(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	// "Basic ..." (wrong scheme), bare token (no scheme), "Bearer" (no space),
	// and "Bearer " (empty credential) must all be rejected.
	for _, hdr := range []string{"Basic " + testAuthToken, testAuthToken, "Bearer", "Bearer "} {
		rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, hdr)
		assert.Equalf(t, http.StatusUnauthorized, rec.Code, "header %q must be rejected", hdr)
	}
}

func TestAuth_ValidTokenAccepted(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, "Bearer "+testAuthToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuth_CaseInsensitiveScheme(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`, "bEaReR "+testAuthToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuth_HealthOpenWithAuthEnabled(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodGet, "/health", "", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuth_LegacyGetRequiresToken(t *testing.T) {
	h := newTestServerWithAuth(t, &stubService{assessment: acceptedAssessment()}, 0, testAuthToken)
	rec := doAuth(t, h, http.MethodGet, "/v1/person@example.com/verification", "", "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = doAuth(t, h, http.MethodGet, "/v1/person@example.com/verification", "", "", "Bearer "+testAuthToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}
