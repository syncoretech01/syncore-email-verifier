package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var checkedAt = time.Date(2026, 7, 13, 19, 5, 29, 0, time.UTC)

// stubService is a deterministic VerificationService. It returns a canned
// assessment, optionally panics, and records the last email it received.
type stubService struct {
	assessment verification.Assessment
	panicNow   bool
	lastEmail  string
}

func (s *stubService) Verify(_ context.Context, email string) verification.Assessment {
	s.lastEmail = email
	if s.panicNow {
		panic("boom")
	}
	a := s.assessment
	a.Email = email
	if a.Result != nil {
		a.Result.Email = email
	}
	return a
}

// defaultTestBatch is the batch config used by the general-purpose test helpers.
var defaultTestBatch = batchConfig{maxItems: 100, concurrency: 10, maxBodyBytes: 65536}

func newTestServer(t *testing.T, svc VerificationService, maxBody int64) http.Handler {
	t.Helper()
	if maxBody == 0 {
		maxBody = 4096
	}
	return newRouter(newHandlers(svc, maxBody, defaultTestBatch, nil, nil, 0), "")
}

// newTestServerWithAuth builds the real router with bearer auth enabled.
func newTestServerWithAuth(t *testing.T, svc VerificationService, maxBody int64, token string) http.Handler {
	t.Helper()
	if maxBody == 0 {
		maxBody = 4096
	}
	return newRouter(newHandlers(svc, maxBody, defaultTestBatch, nil, nil, 0), token)
}

// newTestBatchServer builds the router with a custom batch config for batch tests.
func newTestBatchServer(t *testing.T, svc VerificationService, batch batchConfig) http.Handler {
	t.Helper()
	return newRouter(newHandlers(svc, 4096, batch, nil, nil, 0), "")
}

func do(t *testing.T, h http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
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
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func acceptedAssessment() verification.Assessment {
	smtp := &emailverifier.SMTP{
		HostExists: true, Deliverable: true, CatchAll: false, CatchAllResult: "not_catch_all",
		FullInbox: false, Disabled: false, RecipientResult: "accepted", RecipientReason: "",
		SMTPCode: 250, Source: "smtp",
	}
	res := &emailverifier.Result{
		Email:     "person@example.com",
		Reachable: "yes",
		Syntax:    emailverifier.Syntax{Username: "person", Domain: "example.com", Valid: true},
		SMTP:      smtp, Gravatar: nil, Suggestion: "",
		Disposable: false, RoleAccount: false, Free: false, HasMxRecords: true,
		NullMX: false, MailHostSource: "mx",
	}
	return verification.Assessment{
		Email: "person@example.com", Status: classify.StatusValid, ReasonCode: classify.ReasonSMTPAccepted,
		Retryable: false, Confidence: 95, CheckedAt: checkedAt, Source: "smtp",
		Syntax:          res.Syntax,
		Domain:          verification.DomainEvidence{HasMXRecords: true, MailHostSource: "mx"},
		Account:         verification.AccountEvidence{},
		SMTP:            smtp,
		SMTPAttempted:   true,
		SMTPCheckReason: classify.CheckAttempted,
		Error:           nil,
		Result:          res,
	}
}

func simpleAssessment(status classify.Status, reason classify.ReasonCode, errInfo *verification.ErrorInfo) verification.Assessment {
	res := &emailverifier.Result{
		Reachable: "unknown",
		Syntax:    emailverifier.Syntax{Valid: status != classify.StatusInvalid},
	}
	return verification.Assessment{
		Status: status, ReasonCode: reason, CheckedAt: checkedAt,
		Result: res, Error: errInfo, SMTPCheckReason: classify.CheckAttempted,
	}
}

func TestHealth(t *testing.T) {
	h := newTestServer(t, &stubService{}, 0)
	rec := do(t, h, http.MethodGet, "/health", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestPOST_ValidResult200(t *testing.T) {
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
	rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var dto verificationDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "valid", dto.Status)
	assert.Equal(t, "smtp_accepted", dto.ReasonCode)
	assert.Equal(t, 95, dto.Confidence)
	assert.Equal(t, "2026-07-13T19:05:29Z", dto.CheckedAt)
	assert.Equal(t, "smtp", dto.Source)
	assert.True(t, dto.SMTP.Deliverable)
	assert.Equal(t, 250, dto.SMTP.SMTPCode)
	assert.True(t, dto.SMTP.SMTPAttempted)
	assert.Equal(t, "attempted", dto.SMTP.SMTPCheckReason)
	assert.Equal(t, "not_catch_all", dto.SMTP.CatchAllResult)
	assert.Nil(t, dto.Error)
}

func TestPOST_ClassificationsAllReturn200(t *testing.T) {
	cases := []struct {
		name   string
		assess verification.Assessment
		status string
		reason string
	}{
		{"invalid syntax", simpleAssessment(classify.StatusInvalid, classify.ReasonSyntaxInvalid, &verification.ErrorInfo{Code: "input", Message: "email address has invalid syntax"}), "invalid", "syntax_invalid"},
		{"risky", simpleAssessment(classify.StatusRisky, classify.ReasonDisposableDomain, nil), "risky", "disposable_domain"},
		{"unknown timeout", simpleAssessment(classify.StatusUnknown, classify.ReasonSMTPTimeout, &verification.ErrorInfo{Code: "smtp", Message: "connection to the mail server timed out"}), "unknown", "smtp_timeout"},
		{"null mx", simpleAssessment(classify.StatusInvalid, classify.ReasonNullMX, &verification.ErrorInfo{Code: "mx", Message: "domain does not accept email (null MX)"}), "invalid", "null_mx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestServer(t, &stubService{assessment: tc.assess}, 0)
			rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"x@y.com"}`)
			assert.Equal(t, http.StatusOK, rec.Code)
			var dto verificationDTO
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
			assert.Equal(t, tc.status, dto.Status)
			assert.Equal(t, tc.reason, dto.ReasonCode)
		})
	}
}

func TestGET_RetainsLegacyFields(t *testing.T) {
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
	rec := do(t, h, http.MethodGet, "/v1/person@example.com/verification", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))

	// Legacy fields.
	for _, k := range []string{"email", "reachable", "syntax", "smtp", "gravatar", "suggestion", "disposable", "role_account", "free", "has_mx_records"} {
		assert.Contains(t, m, k, "missing legacy field %q", k)
	}
	// Phase 1A additive top-level fields.
	for _, k := range []string{"null_mx", "mail_host_source"} {
		assert.Contains(t, m, k, "missing additive field %q", k)
	}
	// Appended classification fields.
	for _, k := range []string{"status", "reason_code", "retryable", "confidence", "checked_at", "smtp_attempted", "smtp_check_reason", "source", "error"} {
		assert.Contains(t, m, k, "missing appended field %q", k)
	}
	// Additive nested SMTP fields.
	var smtp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(m["smtp"], &smtp))
	for _, k := range []string{"recipient_result", "recipient_reason", "smtp_code", "catch_all_result", "source"} {
		assert.Contains(t, smtp, k, "missing additive smtp field %q", k)
	}
}

func TestGET_InvalidSyntaxIs200(t *testing.T) {
	assess := simpleAssessment(classify.StatusInvalid, classify.ReasonSyntaxInvalid, &verification.ErrorInfo{Code: "input", Message: "email address has invalid syntax"})
	h := newTestServer(t, &stubService{assessment: assess}, 0)
	rec := do(t, h, http.MethodGet, "/v1/notanemail/verification", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	assert.Equal(t, "invalid", m["status"])
	assert.Equal(t, "syntax_invalid", m["reason_code"])
}

func TestPOST_OversizedBody413(t *testing.T) {
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 16) // tiny limit
	big := `{"email":"` + strings.Repeat("a", 100) + `@example.com"}`
	rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assertErrorEnvelope(t, rec, "payload_too_large")
}

func TestPOST_ContentTypeHandling(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		wantCode    int
	}{
		{"missing content-type", "", http.StatusUnsupportedMediaType},
		{"wrong content-type", "text/plain", http.StatusUnsupportedMediaType},
		{"json", "application/json", http.StatusOK},
		{"json with charset", "application/json; charset=utf-8", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
			rec := do(t, h, http.MethodPost, "/v1/verifications", tc.contentType, `{"email":"person@example.com"}`)
			assert.Equal(t, tc.wantCode, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		})
	}
}

func TestPOST_BadRequests400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{"email":`},
		{"unknown field", `{"email":"a@b.com","extra":1}`},
		{"trailing json", `{"email":"a@b.com"}{"email":"c@d.com"}`},
		{"missing email", `{}`},
		{"empty email", `{"email":""}`},
		{"whitespace only email", `{"email":"   "}`},
		{"over 254 bytes", `{"email":"` + strings.Repeat("a", 255) + `"}`},
		{"control character", `{"email":"a\u0001@b.com"}`},
		{"del character", `{"email":"a\u007f@b.com"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
			rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorEnvelope(t, rec, "invalid_request")
		})
	}
}

func TestPOST_TrailingData(t *testing.T) {
	const obj = `{"email":"person@example.com"}`
	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"object then spaces", obj + "   ", http.StatusOK},
		{"object then tab", obj + "\t", http.StatusOK},
		{"object then crlf", obj + "\r\n", http.StatusOK},
		{"object then object", obj + `{"email":"x@y.com"}`, http.StatusBadRequest},
		{"object then true", obj + "true", http.StatusBadRequest},
		{"object then number", obj + "42", http.StatusBadRequest},
		{"object then array", obj + "[]", http.StatusBadRequest},
		{"object then null", obj + "null", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)
			rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", tc.body)
			assert.Equal(t, tc.wantCode, rec.Code)
			if tc.wantCode == http.StatusBadRequest {
				assertErrorEnvelope(t, rec, "invalid_request")
			}
		})
	}
}

func TestGET_PathProtections400(t *testing.T) {
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0)

	// Over 254 bytes.
	long := strings.Repeat("a", 255)
	rec := do(t, h, http.MethodGet, "/v1/"+long+"/verification", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertErrorEnvelope(t, rec, "invalid_request")

	// Control character (percent-encoded DEL, decoded by net/http to 0x7f).
	rec = do(t, h, http.MethodGet, "/v1/a%7Fb/verification", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertErrorEnvelope(t, rec, "invalid_request")
}

func TestUnknownRoute404(t *testing.T) {
	h := newTestServer(t, &stubService{}, 0)
	rec := do(t, h, http.MethodGet, "/does/not/exist", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assertErrorEnvelope(t, rec, "not_found")
}

func TestWrongMethod405WithAllow(t *testing.T) {
	h := newTestServer(t, &stubService{}, 0)
	rec := do(t, h, http.MethodPut, "/v1/verifications", "", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Header().Get("Allow"), http.MethodPost)
	assertErrorEnvelope(t, rec, "method_not_allowed")
}

func TestPanicRecovery500(t *testing.T) {
	h := newTestServer(t, &stubService{panicNow: true}, 0)
	rec := do(t, h, http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	code := assertErrorEnvelope(t, rec, "internal_error")
	_ = code
	// The panic value must not leak to the client.
	assert.NotContains(t, rec.Body.String(), "boom")
}

func TestNilOptionalEvidenceDoesNotPanic(t *testing.T) {
	// Assessment with nil SMTP, nil Result, nil Error, empty Source.
	assess := verification.Assessment{
		Status: classify.StatusUnknown, ReasonCode: classify.ReasonSMTPDisabled, CheckedAt: checkedAt,
		SMTPCheckReason: classify.CheckDisabled,
		SMTP:            nil, Result: nil, Error: nil, Source: "",
	}

	// POST must not panic.
	hp := newTestServer(t, &stubService{assessment: assess}, 0)
	recP := do(t, hp, http.MethodPost, "/v1/verifications", "application/json", `{"email":"x@y.com"}`)
	assert.Equal(t, http.StatusOK, recP.Code)
	var dto verificationDTO
	require.NoError(t, json.Unmarshal(recP.Body.Bytes(), &dto))
	assert.Equal(t, "unknown", dto.Status)
	assert.False(t, dto.SMTP.SMTPAttempted)
	assert.False(t, dto.SMTP.Deliverable) // not fabricated

	// GET must not panic with a nil legacy Result.
	hg := newTestServer(t, &stubService{assessment: assess}, 0)
	recG := do(t, hg, http.MethodGet, "/v1/x@y.com/verification", "", "")
	assert.Equal(t, http.StatusOK, recG.Code)
}

// TestEveryResponseIsJSON sweeps representative endpoints/errors and asserts the
// Content-Type is application/json on all of them.
func TestEveryResponseIsJSON(t *testing.T) {
	h := newTestServer(t, &stubService{assessment: acceptedAssessment()}, 16)
	reqs := []struct {
		method, target, ct, body string
	}{
		{http.MethodGet, "/health", "", ""},
		{http.MethodGet, "/v1/person@example.com/verification", "", ""},
		{http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`}, // 413 (tiny limit)
		{http.MethodPost, "/v1/verifications", "text/plain", `x`},                                    // 415
		{http.MethodGet, "/nope", "", ""},                                                            // 404
		{http.MethodPut, "/v1/verifications", "", ""},                                                // 405
	}
	for _, rq := range reqs {
		rec := do(t, h, rq.method, rq.target, rq.ct, rq.body)
		assert.Equalf(t, "application/json", rec.Header().Get("Content-Type"), "%s %s", rq.method, rq.target)
	}
}

func assertErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) string {
	t.Helper()
	var env errorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, wantCode, env.Error.Code)
	assert.NotEmpty(t, env.Error.Message)
	return env.Error.Code
}
