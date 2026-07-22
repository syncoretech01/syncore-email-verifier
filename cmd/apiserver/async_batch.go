package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
)

// Async batch endpoints live under /batches (not /v1/) because httprouter's
// legacy GET /v1/:email/verification param forbids a static GET sibling under
// /v1/. They require auth like every non-/health route.

const defaultResultsPageLimit = 100

type asyncBatchRequest struct {
	Emails      []string        `json:"emails"`
	CallbackURL string          `json:"callback_url,omitempty"`
	Meta        json.RawMessage `json:"meta,omitempty"`
}

type asyncBatchSubmitResponse struct {
	BatchID string `json:"batch_id"`
	State   string `json:"state"`
	Total   int    `json:"total"`
}

// handleBatchSubmit accepts a bounded list and enqueues it for async processing.
func (h *Handlers) handleBatchSubmit(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "async batch processing is not enabled")
		return
	}
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.batch.maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req asyncBatchRequest
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a single valid JSON object")
		return
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain exactly one JSON object")
		return
	}
	if req.Emails == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "emails field is required")
		return
	}
	if len(req.Emails) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "emails must not be empty")
		return
	}
	if len(req.Emails) > h.asyncBatchMaxItems {
		writeError(w, http.StatusBadRequest, "invalid_request", "emails must not exceed the async batch limit")
		return
	}
	if req.CallbackURL != "" && !isHTTPURL(req.CallbackURL) {
		writeError(w, http.StatusBadRequest, "invalid_request", "callback_url must be an http(s) URL")
		return
	}

	b, err := h.jobs.Submit(req.Emails, req.CallbackURL, req.Meta)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "the service is shutting down")
		return
	}
	writeJSON(w, http.StatusAccepted, asyncBatchSubmitResponse{BatchID: b.ID, State: string(b.State), Total: b.Total})
}

// handleBatchStatus returns a batch's progress.
func (h *Handlers) handleBatchStatus(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "async batch processing is not enabled")
		return
	}
	b, ok := h.jobs.Get(ps.ByName("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

type asyncBatchResultsResponse struct {
	BatchID string         `json:"batch_id"`
	Total   int            `json:"total"`
	Offset  int            `json:"offset"`
	Limit   int            `json:"limit"`
	Results []asyncItemDTO `json:"results"`
}

type asyncItemDTO struct {
	Email    string          `json:"email"`
	Attempts int             `json:"attempts"`
	Result   verificationDTO `json:"result"`
}

// handleBatchResults returns a page of a batch's results.
func (h *Handlers) handleBatchResults(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	if h.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "async batch processing is not enabled")
		return
	}
	id := ps.ByName("id")
	offset := queryInt(r, "offset", 0)
	limit := queryInt(r, "limit", defaultResultsPageLimit)
	if limit <= 0 {
		limit = defaultResultsPageLimit
	}

	items, total, ok := h.jobs.Results(id, offset, limit)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	out := make([]asyncItemDTO, len(items))
	for i, it := range items {
		out[i] = asyncItemDTO{Email: it.Email, Attempts: it.Attempts, Result: toVerification(it.Assessment)}
	}
	writeJSON(w, http.StatusOK, asyncBatchResultsResponse{BatchID: id, Total: total, Offset: offset, Limit: limit, Results: out})
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// isHTTPURL is a light guard: the callback must look like an http(s) URL.
func isHTTPURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}
