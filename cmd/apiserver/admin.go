package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/julienschmidt/httprouter"
)

type erasureRequest struct {
	Email string `json:"email"`
}

// handleErasure removes an address's cached verification data (GDPR/CCPA
// right-to-erasure) and records a hashed-email audit event. Lives under /admin/
// to avoid httprouter's /v1/:email conflict.
func (h *Handlers) handleErasure(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if h.erase == nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "erasure is not enabled")
		return
	}
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req erasureRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a single valid JSON object")
		return
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain exactly one JSON object")
		return
	}
	trimmed, msg := validateEmailInput(req.Email)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}

	if err := h.erase(r.Context(), trimmed); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erasure failed")
		return
	}
	h.audit("erasure", trimmed)
	writeJSON(w, http.StatusOK, map[string]bool{"erased": true})
}

// audit emits a compliance audit event with a hashed email — never plaintext PII.
func (h *Handlers) audit(action, email string) {
	if h.logger == nil {
		return
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	h.logger.Info("audit", "action", action, "email_sha256", hex.EncodeToString(sum[:]))
}
