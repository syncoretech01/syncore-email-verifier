package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/AfterShip/email-verifier/internal/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAsyncServer builds a router with a live async-batch manager over svc.
func newAsyncServer(t *testing.T, svc VerificationService, maxItems int) http.Handler {
	t.Helper()
	mgr := jobs.NewManager(svc, jobs.Config{Workers: 2})
	mgr.Start()
	t.Cleanup(mgr.Stop)
	return newRouter(newHandlers(svc, 4096, defaultTestBatch, nil, mgr, maxItems), "")
}

func pollBatchDone(t *testing.T, h http.Handler, id string) jobs.Batch {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := do(t, h, http.MethodGet, "/batches/"+id, "", "")
		require.Equal(t, http.StatusOK, s.Code)
		var st jobs.Batch
		require.NoError(t, json.Unmarshal(s.Body.Bytes(), &st))
		if st.State == jobs.StateDone {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("batch %s did not reach done", id)
	return jobs.Batch{}
}

func TestAsyncBatch_SubmitStatusResults(t *testing.T) {
	h := newAsyncServer(t, okService(), 100)

	rec := do(t, h, http.MethodPost, "/batches", "application/json", `{"emails":["a@x.com","b@x.com"],"meta":{"list":"1"}}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var sub asyncBatchSubmitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	assert.NotEmpty(t, sub.BatchID)
	assert.Equal(t, "queued", sub.State)
	assert.Equal(t, 2, sub.Total)

	status := pollBatchDone(t, h, sub.BatchID)
	assert.Equal(t, 2, status.Counts["valid"])
	assert.Equal(t, 2, status.Done)

	r := do(t, h, http.MethodGet, "/batches/"+sub.BatchID+"/results?offset=0&limit=10", "", "")
	require.Equal(t, http.StatusOK, r.Code)
	var res asyncBatchResultsResponse
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &res))
	assert.Equal(t, 2, res.Total)
	require.Len(t, res.Results, 2)
	assert.Equal(t, "a@x.com", res.Results[0].Email)
	assert.Equal(t, "valid", res.Results[0].Result.Status)
}

func TestAsyncBatch_ResultsPagination(t *testing.T) {
	h := newAsyncServer(t, okService(), 100)
	rec := do(t, h, http.MethodPost, "/batches", "application/json",
		`{"emails":["a@x.com","b@x.com","c@x.com","d@x.com","e@x.com"]}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var sub asyncBatchSubmitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	pollBatchDone(t, h, sub.BatchID)

	r := do(t, h, http.MethodGet, "/batches/"+sub.BatchID+"/results?offset=2&limit=2", "", "")
	require.Equal(t, http.StatusOK, r.Code)
	var res asyncBatchResultsResponse
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &res))
	assert.Equal(t, 5, res.Total)
	require.Len(t, res.Results, 2)
	assert.Equal(t, "c@x.com", res.Results[0].Email)
	assert.Equal(t, "d@x.com", res.Results[1].Email)
}

func TestAsyncBatch_StatusNotFound(t *testing.T) {
	h := newAsyncServer(t, okService(), 100)
	rec := do(t, h, http.MethodGet, "/batches/deadbeef", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assertErrorEnvelope(t, rec, "not_found")
}

func TestAsyncBatch_Validation(t *testing.T) {
	h := newAsyncServer(t, okService(), 3)
	cases := []struct{ name, body string }{
		{"missing emails", `{"meta":{}}`},
		{"empty array", `{"emails":[]}`},
		{"over cap", `{"emails":["a@x.com","b@x.com","c@x.com","d@x.com"]}`},
		{"bad callback", `{"emails":["a@x.com"],"callback_url":"ftp://x"}`},
		{"unknown field", `{"emails":["a@x.com"],"nope":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/batches", "application/json", tc.body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertErrorEnvelope(t, rec, "invalid_request")
		})
	}
}

func TestAsyncBatch_ContentType415(t *testing.T) {
	h := newAsyncServer(t, okService(), 100)
	rec := do(t, h, http.MethodPost, "/batches", "text/plain", `{"emails":["a@x.com"]}`)
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

func TestAsyncBatch_DisabledWhenNoManager(t *testing.T) {
	// Default helper passes a nil jobs manager -> the /batches API is unavailable.
	h := newTestServer(t, okService(), 0)
	rec := do(t, h, http.MethodPost, "/batches", "application/json", `{"emails":["a@x.com"]}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assertErrorEnvelope(t, rec, "not_available")
}
