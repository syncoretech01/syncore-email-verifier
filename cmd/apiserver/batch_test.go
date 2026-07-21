package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/classify"
	"github.com/AfterShip/email-verifier/internal/config"
	"github.com/AfterShip/email-verifier/internal/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// funcService is a VerificationService backed by a function, for flexible per-
// email behavior in batch tests.
type funcService struct {
	fn func(ctx context.Context, email string) verification.Assessment
}

func (f funcService) Verify(ctx context.Context, email string) verification.Assessment {
	return f.fn(ctx, email)
}

// okService returns a valid assessment for every email.
func okService() funcService {
	return funcService{fn: func(_ context.Context, email string) verification.Assessment {
		a := simpleAssessment(classify.StatusValid, classify.ReasonSMTPAccepted, nil)
		a.Email = email
		return a
	}}
}

func decodeBatch(t *testing.T, rec *httptest.ResponseRecorder) batchResponse {
	t.Helper()
	var resp batchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestBatch_OrderedResultsAndMeta(t *testing.T) {
	h := newTestServer(t, okService(), 0)
	body := `{"emails":["a@example.com","b@example.com","c@example.com"],"meta":{"batch_id":"xyz"}}`
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", body)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBatch(t, rec)
	require.Len(t, resp.Results, 3)
	assert.Equal(t, "a@example.com", resp.Results[0].Email)
	assert.Equal(t, "b@example.com", resp.Results[1].Email)
	assert.Equal(t, "c@example.com", resp.Results[2].Email)
	assert.JSONEq(t, `{"batch_id":"xyz"}`, string(resp.Meta))
}

func TestBatch_NoMetaOmitsMeta(t *testing.T) {
	h := newTestServer(t, okService(), 0)
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", `{"emails":["a@example.com"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	// meta must be absent from the response body when not supplied.
	assert.NotContains(t, rec.Body.String(), `"meta"`)
}

func TestBatch_PerItemInvalidDoesNotFailBatch(t *testing.T) {
	svc := funcService{fn: func(_ context.Context, email string) verification.Assessment {
		// Model the classifier: a malformed address is invalid syntax, not a fault.
		if !strings.Contains(email, "@") {
			a := simpleAssessment(classify.StatusInvalid, classify.ReasonSyntaxInvalid,
				&verification.ErrorInfo{Code: "input", Message: "email address has invalid syntax"})
			a.Email = email
			return a
		}
		a := simpleAssessment(classify.StatusValid, classify.ReasonSMTPAccepted, nil)
		a.Email = email
		return a
	}}
	h := newTestServer(t, svc, 0)
	body := `{"emails":["good@example.com","not-an-email","also@example.com"]}`
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", body)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBatch(t, rec)
	require.Len(t, resp.Results, 3)
	assert.Equal(t, "valid", resp.Results[0].Status)
	assert.Equal(t, "invalid", resp.Results[1].Status)
	assert.Equal(t, "syntax_invalid", resp.Results[1].ReasonCode)
	assert.Equal(t, "valid", resp.Results[2].Status)
}

func TestBatch_PerItemFaultBecomesUnknown(t *testing.T) {
	svc := funcService{fn: func(_ context.Context, email string) verification.Assessment {
		if email == "boom@example.com" {
			panic("simulated item fault")
		}
		a := simpleAssessment(classify.StatusValid, classify.ReasonSMTPAccepted, nil)
		a.Email = email
		return a
	}}
	h := newTestServer(t, svc, 0)
	body := `{"emails":["ok@example.com","boom@example.com","fine@example.com"]}`
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", body)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBatch(t, rec)
	require.Len(t, resp.Results, 3)
	assert.Equal(t, "valid", resp.Results[0].Status)
	assert.Equal(t, "unknown", resp.Results[1].Status, "a faulty item becomes unknown")
	assert.True(t, resp.Results[1].Retryable, "a faulty item is retryable")
	assert.Equal(t, "valid", resp.Results[2].Status)
}

func TestBatch_RequestValidation(t *testing.T) {
	h := newTestBatchServer(t, okService(), batchConfig{maxItems: 3, concurrency: 2, maxBodyBytes: 65536})
	cases := []struct{ name, body string }{
		{"missing emails", `{"meta":{}}`},
		{"empty array", `{"emails":[]}`},
		{"over cap", `{"emails":["a@x.com","b@x.com","c@x.com","d@x.com"]}`},
		{"unknown field", `{"emails":["a@x.com"],"foo":1}`},
		{"trailing data", `{"emails":["a@x.com"]}{}`},
		{"malformed json", `{"emails":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorEnvelope(t, rec, "invalid_request")
		})
	}
}

func TestBatch_ContentType415(t *testing.T) {
	h := newTestServer(t, okService(), 0)
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "text/plain", `{"emails":["a@x.com"]}`)
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	assertErrorEnvelope(t, rec, "unsupported_media_type")
}

func TestBatch_Oversize413(t *testing.T) {
	h := newTestBatchServer(t, okService(), batchConfig{maxItems: 100, concurrency: 10, maxBodyBytes: 16})
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", `{"emails":["averylongaddress@example.com"]}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assertErrorEnvelope(t, rec, "payload_too_large")
}

func TestBatch_AuthRequired(t *testing.T) {
	h := newRouter(newHandlers(okService(), 4096, defaultTestBatch, nil, nil, 0), testAuthToken)
	rec := doAuth(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", `{"emails":["a@x.com"]}`, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = doAuth(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", `{"emails":["a@x.com"]}`, "Bearer "+testAuthToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestBatch_ConcurrencyBound proves the worker pool bounds wall-clock to roughly
// ceil(N/concurrency) rounds, not N sequential items. It uses a small simulated
// per-item latency so the test is fast and deterministic (no network).
func TestBatch_ConcurrencyBound(t *testing.T) {
	const (
		perItem     = 30 * time.Millisecond
		n           = 100
		concurrency = 10
	)
	svc := funcService{fn: func(_ context.Context, email string) verification.Assessment {
		time.Sleep(perItem)
		a := simpleAssessment(classify.StatusValid, classify.ReasonSMTPAccepted, nil)
		a.Email = email
		return a
	}}
	h := newTestBatchServer(t, svc, batchConfig{maxItems: n, concurrency: concurrency, maxBodyBytes: 1 << 20})

	emails := make([]string, n)
	for i := range emails {
		emails[i] = fmt.Sprintf("user%d@example.com", i)
	}
	bodyBytes, err := json.Marshal(batchRequest{Emails: emails})
	require.NoError(t, err)

	start := time.Now()
	rec := do(t, h, http.MethodPost, "/v1/verifications:batch", "application/json", string(bodyBytes))
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBatch(t, rec)
	require.Len(t, resp.Results, n)
	for i := range emails {
		assert.Equalf(t, emails[i], resp.Results[i].Email, "results must preserve input order at index %d", i)
	}

	serial := time.Duration(n) * perItem // 3s if run one-at-a-time
	assert.Lessf(t, elapsed, serial/3, "batch took %s; the worker pool should keep it far below the serial %s", elapsed, serial)
}

func TestWriteTimeoutFor_BatchAware(t *testing.T) {
	cfg := &config.Config{
		ConnectTimeout:   10 * time.Second,
		OperationTimeout: 10 * time.Second,
		BatchMaxItems:    100,
		BatchConcurrency: 10,
	}
	// ceil(100/10)=10 rounds * (10s+10s) = 200s, + 15s headroom = 215s.
	assert.Equal(t, 215*time.Second, writeTimeoutFor(cfg))
}

func TestWriteTimeoutFor_SingleFloor(t *testing.T) {
	cfg := &config.Config{
		ConnectTimeout:   2 * time.Second,
		OperationTimeout: 2 * time.Second,
		BatchMaxItems:    1,
		BatchConcurrency: 10,
	}
	// Both single (19s) and batch (1 round -> 4s+15s=19s) are below the 35s floor.
	assert.Equal(t, 35*time.Second, writeTimeoutFor(cfg))
}
