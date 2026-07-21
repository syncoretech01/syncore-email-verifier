package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/feedback"
	"github.com/AfterShip/email-verifier/internal/jobs"
)

type feedbackEventDTO struct {
	Email string `json:"email"`
	Type  string `json:"type"`
}

// handleFeedback ingests a signed sending-outcome event (bounce/complaint/
// delivered/engaged) into the per-domain reputation store. The body must carry a
// valid HMAC-SHA256 signature in X-Syncore-Signature, computed with the feedback
// signing key — so only the CRM/ESP forwarder can post outcomes.
func (h *Handlers) handleFeedback(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if h.feedbackStore == nil || len(h.feedbackKey) == 0 {
		writeError(w, http.StatusServiceUnavailable, "not_available", "feedback ingestion is not enabled")
		return
	}
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		return
	}

	// Verify the signature over the raw body (constant-time).
	expected := jobs.Sign(h.feedbackKey, body)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(jobs.SignatureHeader)), []byte(expected)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing signature")
		return
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var ev feedbackEventDTO
	if err := dec.Decode(&ev); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a single valid JSON object")
		return
	}
	if ev.Email == "" || ev.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "email and type are required")
		return
	}

	h.feedbackStore.Record(feedback.Event{Email: ev.Email, Type: feedback.EventType(ev.Type)})
	h.audit("feedback", ev.Email)
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
