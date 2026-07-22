package main

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/feedback"
)

// The provider adapters accept RAW Amazon SES (over SNS) and Smartlead webhook
// payloads directly, normalize them into feedback events, and record them —
// removing the need for an external re-signing forwarder.
//
// Auth: a shared secret (SYNCORE_VERIFIER_FEEDBACK_ADAPTER_TOKEN) passed either
// in the `X-Syncore-Token` header or a `?token=` query parameter (compared in
// constant time). A query parameter is supported because Amazon SNS HTTPS
// subscriptions cannot set custom request headers — put the token in the
// subscription URL. Note: SNS delivers notifications with
// `Content-Type: text/plain`, so these endpoints do not require a JSON content
// type; the body is read and parsed directly.
//
// Full SNS message-signature verification (fetching the signing cert chain) is a
// future hardening; the shared-secret gate keeps the endpoint testable and
// closed to the public in the meantime.

// adapterAuthorized reports whether the request carries the correct adapter token.
func (h *Handlers) adapterAuthorized(r *http.Request) bool {
	provided := r.Header.Get("X-Syncore-Token")
	if provided == "" {
		provided = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(provided), h.feedbackAdapterToken) == 1
}

// readAdapterBody enforces the body-size limit and returns the raw bytes.
func (h *Handlers) readAdapterBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		return nil, false
	}
	return body, true
}

// adapterEnabled reports whether the provider adapters are configured.
func (h *Handlers) adapterEnabled() bool {
	return h.feedbackStore != nil && len(h.feedbackAdapterToken) > 0
}

// handleFeedbackSES ingests Amazon SES events delivered over SNS.
func (h *Handlers) handleFeedbackSES(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !h.adapterEnabled() {
		writeError(w, http.StatusServiceUnavailable, "not_available", "feedback ingestion is not enabled")
		return
	}
	if !h.adapterAuthorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing token")
		return
	}
	body, ok := h.readAdapterBody(w, r)
	if !ok {
		return
	}

	res, err := feedback.ParseSES(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not parse SES/SNS payload")
		return
	}
	if res.SubscriptionConfirmation {
		// One-time SNS subscription handshake: surface the SubscribeURL so an
		// operator can confirm it. (Auto-confirm is a future enhancement.)
		h.logger.Info("received SNS subscription confirmation; confirm it to activate",
			"subscribe_url", res.SubscribeURL)
		writeJSON(w, http.StatusOK, map[string]any{"subscription_confirmation": true})
		return
	}

	h.recordFeedback(res.Events)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "recorded": len(res.Events)})
}

// handleFeedbackSmartlead ingests Smartlead webhook events.
func (h *Handlers) handleFeedbackSmartlead(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !h.adapterEnabled() {
		writeError(w, http.StatusServiceUnavailable, "not_available", "feedback ingestion is not enabled")
		return
	}
	if !h.adapterAuthorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing token")
		return
	}
	body, ok := h.readAdapterBody(w, r)
	if !ok {
		return
	}

	events, err := feedback.ParseSmartlead(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not parse Smartlead payload")
		return
	}
	h.recordFeedback(events)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "recorded": len(events)})
}

// recordFeedback folds normalized events into the reputation store and audits
// each (audit logs carry only a hash of the email, never the address).
func (h *Handlers) recordFeedback(events []feedback.Event) {
	for _, e := range events {
		h.feedbackStore.Record(e)
		h.audit("feedback", e.Email)
	}
}
