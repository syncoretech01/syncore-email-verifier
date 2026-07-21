package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/feedback"
	"github.com/AfterShip/email-verifier/internal/jobs"
	"github.com/AfterShip/email-verifier/internal/metrics"
	"github.com/AfterShip/email-verifier/internal/ratelimit"
	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/AfterShip/email-verifier/internal/verification"
)

// maxEmailBytes is the RFC 5321 maximum length of an email address.
const maxEmailBytes = 254

// Idempotency-Key handling for POST /v1/verifications.
const (
	idempotencyTTL          = 24 * time.Hour
	maxIdempotencyKeyBytes  = 255
	idempotencyKeyNamespace = "idem:"
)

// VerificationService is the behavior the handlers need. The Phase 1B
// *verification.Service satisfies it; tests substitute a stub.
type VerificationService interface {
	Verify(ctx context.Context, email string) verification.Assessment
}

// batchConfig bounds the batch endpoint.
type batchConfig struct {
	maxItems     int
	concurrency  int
	maxBodyBytes int64
}

// Handlers holds the dependencies for the HTTP handlers.
type Handlers struct {
	svc          VerificationService
	maxBodyBytes int64
	batch        batchConfig
	// idempotency memoizes POST results by Idempotency-Key so a retried CRM call
	// returns the same result without re-verifying. nil disables the feature.
	idempotency store.Store[verification.Assessment]
	// jobs runs asynchronous batch verifications. nil disables the /batches API.
	jobs               *jobs.Manager
	asyncBatchMaxItems int
	// Observability. metrics/ready may be nil (feature off); logger defaults.
	metrics *metrics.Registry
	logger  *slog.Logger
	ready   func(context.Context) error
	// rateLimiter is nil when rate limiting is disabled.
	rateLimiter *ratelimit.Limiter
	// apiKeyHashes maps sha256(key) hex -> client name; additional accepted creds.
	apiKeyHashes map[string]string
	// erase removes an address's cached data (right-to-erasure). nil disables it.
	erase func(ctx context.Context, email string) error
	// feedbackStore ingests sending outcomes; feedbackKey signs the ingestion
	// endpoint. Both empty/nil disables POST /v1/feedback.
	feedbackStore *feedback.Store
	feedbackKey   []byte
}

// handlerOpts are the dependencies for the HTTP handlers. Optional fields may be
// their zero value.
type handlerOpts struct {
	svc                VerificationService
	maxBodyBytes       int64
	batch              batchConfig
	idempotency        store.Store[verification.Assessment]
	jobs               *jobs.Manager
	asyncBatchMaxItems int
	metrics            *metrics.Registry
	logger             *slog.Logger
	ready              func(context.Context) error
	rateLimiter        *ratelimit.Limiter
	apiKeyHashes       map[string]string
	erase              func(ctx context.Context, email string) error
	feedbackStore      *feedback.Store
	feedbackKey        []byte
}

func newHandlers(o handlerOpts) *Handlers {
	if o.batch.maxItems <= 0 {
		o.batch.maxItems = 100
	}
	if o.batch.concurrency <= 0 {
		o.batch.concurrency = 10
	}
	if o.batch.maxBodyBytes <= 0 {
		o.batch.maxBodyBytes = 65536
	}
	if o.asyncBatchMaxItems <= 0 {
		o.asyncBatchMaxItems = 10000
	}
	if o.maxBodyBytes <= 0 {
		o.maxBodyBytes = 4096
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return &Handlers{
		svc:                o.svc,
		maxBodyBytes:       o.maxBodyBytes,
		batch:              o.batch,
		idempotency:        o.idempotency,
		jobs:               o.jobs,
		asyncBatchMaxItems: o.asyncBatchMaxItems,
		metrics:            o.metrics,
		logger:             o.logger,
		ready:              o.ready,
		rateLimiter:        o.rateLimiter,
		apiKeyHashes:       o.apiKeyHashes,
		erase:              o.erase,
		feedbackStore:      o.feedbackStore,
		feedbackKey:        o.feedbackKey,
	}
}

// handleHealth is a liveness endpoint. It performs no DNS/SMTP/provider checks.
func (h *Handlers) handleHealth(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetVerification preserves the legacy GET endpoint, returning the
// extended legacy-compatible response.
func (h *Handlers) handleGetVerification(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	// net/http has already URL-decoded the path, so ps.ByName yields the decoded
	// address. Apply the 254-byte / control-character / trim protections here.
	trimmed, msg := validateEmailInput(ps.ByName("email"))
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	a := h.svc.Verify(r.Context(), trimmed)
	h.recordVerification(string(a.Status))
	writeJSON(w, http.StatusOK, toLegacyResponse(a))
}

// handleVerifications implements the structured POST endpoint.
func (h *Handlers) handleVerifications(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req struct {
		Email *string `json:"email"`
	}
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a single valid JSON object")
		return
	}
	// Reject any trailing data after the JSON object: a second decode must reach
	// io.EOF. This rejects a second object or a trailing true/number/array/null/
	// etc., while allowing insignificant trailing whitespace (spaces, tabs, CR, LF).
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain exactly one JSON object")
		return
	}
	if req.Email == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "email field is required")
		return
	}
	trimmed, msg := validateEmailInput(*req.Email)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}

	// Idempotency: a repeated request with the same Idempotency-Key returns the
	// stored result without re-verifying.
	idemKey := idempotencyKey(r.Header.Get("Idempotency-Key"))
	if idemKey != "" && h.idempotency != nil {
		if a, ok, err := h.idempotency.Get(r.Context(), idemKey); err == nil && ok {
			h.recordVerification(string(a.Status))
			writeJSON(w, http.StatusOK, toVerification(a))
			return
		}
	}

	a := h.svc.Verify(r.Context(), trimmed)
	h.recordVerification(string(a.Status))
	h.audit("verification", trimmed)

	if idemKey != "" && h.idempotency != nil {
		if err := h.idempotency.Set(r.Context(), idemKey, a, idempotencyTTL); err != nil {
			log.Printf("idempotency store set failed: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, toVerification(a))
}

// idempotencyKey sanitizes the Idempotency-Key header and namespaces it. Returns
// "" (idempotency skipped) for an empty, over-long, or control-bearing value.
func idempotencyKey(raw string) string {
	k := strings.TrimSpace(raw)
	if k == "" || len(k) > maxIdempotencyKeyBytes || strings.IndexFunc(k, isControlRune) >= 0 {
		return ""
	}
	return idempotencyKeyNamespace + k
}

// batchRequest is the structured batch input. meta is opaque and echoed back.
type batchRequest struct {
	Emails []string        `json:"emails"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

// batchResponse carries one result per input email, in order, plus the echoed
// meta.
type batchResponse struct {
	Results []verificationDTO `json:"results"`
	Meta    json.RawMessage   `json:"meta,omitempty"`
}

// handleVerificationsBatch verifies a bounded list of emails through a bounded
// worker pool. It is stateless: no persistence, no queue. A single bad or faulty
// item never fails the whole batch — results are returned one-per-input, in order.
func (h *Handlers) handleVerificationsBatch(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.batch.maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req batchRequest
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
	if len(req.Emails) > h.batch.maxItems {
		writeError(w, http.StatusBadRequest, "invalid_request", "emails must not exceed the batch limit")
		return
	}

	results := h.verifyBatch(r.Context(), req.Emails)
	writeJSON(w, http.StatusOK, batchResponse{Results: results, Meta: req.Meta})
}

// verifyBatch runs the emails through a bounded worker pool, returning one result
// per input in the original order. Each item is isolated: a per-item panic is
// recovered into an unknown/retryable result so it never fails the batch.
func (h *Handlers) verifyBatch(ctx context.Context, emails []string) []verificationDTO {
	results := make([]verificationDTO, len(emails))

	workers := h.batch.concurrency
	if workers > len(emails) {
		workers = len(emails)
	}

	type job struct {
		i     int
		email string
	}
	jobs := make(chan job)

	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				// Distinct indices → no shared-slice-element races.
				results[j.i] = h.verifyItem(ctx, j.email)
			}
		}()
	}
	for i, email := range emails {
		jobs <- job{i: i, email: email}
	}
	close(jobs)
	wg.Wait()

	return results
}

// verifyItem verifies one address. A panic is recovered into an unknown,
// retryable result (a per-item fault must never fail the batch). Invalid or
// unverifiable inputs flow through the service and classifier normally (e.g.
// syntax_invalid), so no classification semantics are duplicated here.
func (h *Handlers) verifyItem(ctx context.Context, rawEmail string) (dto verificationDTO) {
	email := strings.TrimSpace(rawEmail)
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("recovered panic while verifying a batch item")
			dto = faultItemDTO(email)
		}
	}()
	return toVerification(h.svc.Verify(ctx, email))
}

// faultItemDTO represents an item whose verification could not be completed due
// to an unexpected internal fault: unknown + retryable, never invalid.
func faultItemDTO(email string) verificationDTO {
	return verificationDTO{
		Email:     email,
		Status:    "unknown",
		Retryable: true,
		Error:     &apiError{Code: "internal", Message: "verification could not be completed"},
	}
}

// validateEmailInput trims surrounding whitespace and enforces the request-level
// input protections. It returns the trimmed value and an empty message on
// success, or an empty value and a request-error message on failure.
//
// Note: a value that is merely not a valid email *address* passes here — invalid
// syntax is a completed verification (HTTP 200, syntax_invalid), not a request
// error.
func validateEmailInput(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "email must not be empty"
	}
	if len(trimmed) > maxEmailBytes {
		return "", "email must not exceed 254 bytes"
	}
	if strings.IndexFunc(trimmed, isControlRune) >= 0 {
		return "", "email must not contain control characters"
	}
	return trimmed, ""
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// isJSONMediaType accepts application/json with any valid parameters (e.g.
// charset). A missing or non-JSON media type returns false.
func isJSONMediaType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

// ---- JSON writing + error envelope ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

// ---- Presenters (HTTP DTOs) ----

type syntaxDTO struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Valid    bool   `json:"valid"`
}

type domainDTO struct {
	HasMXRecords   bool             `json:"has_mx_records"`
	NullMX         bool             `json:"null_mx"`
	ImplicitMX     bool             `json:"implicit_mx"`
	MailHostSource string           `json:"mail_host_source"`
	Disposable     bool             `json:"disposable"`
	FreeProvider   bool             `json:"free_provider"`
	Suggestion     string           `json:"suggestion"`
	DomainHealth   *domainHealthDTO `json:"domain_health,omitempty"`
}

type domainHealthDTO struct {
	SPF   bool `json:"spf"`
	DMARC bool `json:"dmarc"`
	MX    bool `json:"mx"`
}

type scoreComponentsDTO struct {
	Syntax  int `json:"syntax"`
	Domain  int `json:"domain"`
	Mailbox int `json:"mailbox"`
}

type accountDTO struct {
	RoleAccount bool `json:"role_account"`
}

type smtpDTO struct {
	HostExists      bool   `json:"host_exists"`
	Deliverable     bool   `json:"deliverable"`
	CatchAll        bool   `json:"catch_all"`
	CatchAllResult  string `json:"catch_all_result"`
	FullInbox       bool   `json:"full_inbox"`
	Disabled        bool   `json:"disabled"`
	RecipientResult string `json:"recipient_result"`
	RecipientReason string `json:"recipient_reason"`
	SMTPCode        int    `json:"smtp_code"`
	SMTPAttempted   bool   `json:"smtp_attempted"`
	SMTPCheckReason string `json:"smtp_check_reason"`
}

// verificationDTO is the structured POST response shape.
type verificationDTO struct {
	Email               string             `json:"email"`
	Status              string             `json:"status"`
	ReasonCode          string             `json:"reason_code"`
	Retryable           bool               `json:"retryable"`
	Confidence          int                `json:"confidence"`
	DeliverabilityScore int                `json:"deliverability_score"`
	ScoreComponents     scoreComponentsDTO `json:"score_components"`
	Suppressed          bool               `json:"suppressed"`
	CheckedAt           string             `json:"checked_at"`
	Source              string             `json:"source"`
	Syntax              syntaxDTO          `json:"syntax"`
	Domain              domainDTO          `json:"domain"`
	Account             accountDTO         `json:"account"`
	SMTP                smtpDTO            `json:"smtp"`
	Error               *apiError          `json:"error"`
}

// legacyResponseDTO is the extended legacy GET response: all legacy + additive
// Phase 1A fields (via the embedded Result) plus the appended classification
// fields.
type legacyResponseDTO struct {
	*emailverifier.Result
	Status              string    `json:"status"`
	ReasonCode          string    `json:"reason_code"`
	Retryable           bool      `json:"retryable"`
	Confidence          int       `json:"confidence"`
	DeliverabilityScore int       `json:"deliverability_score"`
	CheckedAt           string    `json:"checked_at"`
	SMTPAttempted       bool      `json:"smtp_attempted"`
	SMTPCheckReason     string    `json:"smtp_check_reason"`
	Source              string    `json:"source"`
	Error               *apiError `json:"error"`
}

func toVerification(a verification.Assessment) verificationDTO {
	return verificationDTO{
		Email:               a.Email,
		Status:              string(a.Status),
		ReasonCode:          string(a.ReasonCode),
		Retryable:           a.Retryable,
		Confidence:          a.Confidence,
		DeliverabilityScore: a.DeliverabilityScore,
		ScoreComponents: scoreComponentsDTO{
			Syntax:  a.ScoreComponents.Syntax,
			Domain:  a.ScoreComponents.Domain,
			Mailbox: a.ScoreComponents.Mailbox,
		},
		Suppressed: a.Suppressed,
		CheckedAt:  formatCheckedAt(a.CheckedAt),
		Source:     a.Source,
		Syntax: syntaxDTO{
			Username: a.Syntax.Username,
			Domain:   a.Syntax.Domain,
			Valid:    a.Syntax.Valid,
		},
		Domain: domainDTO{
			HasMXRecords:   a.Domain.HasMXRecords,
			NullMX:         a.Domain.NullMX,
			ImplicitMX:     a.Domain.ImplicitMX,
			MailHostSource: a.Domain.MailHostSource,
			Disposable:     a.Domain.Disposable,
			FreeProvider:   a.Domain.FreeProvider,
			Suggestion:     a.Domain.Suggestion,
			DomainHealth:   toDomainHealthDTO(a.Domain.Health),
		},
		Account: accountDTO{RoleAccount: a.Account.RoleAccount},
		SMTP:    toSMTPDTO(a),
		Error:   toAPIError(a.Error),
	}
}

// toDomainHealthDTO maps optional domain-health evidence; nil when the check was
// not performed.
func toDomainHealthDTO(h *verification.DomainHealthEvidence) *domainHealthDTO {
	if h == nil {
		return nil
	}
	return &domainHealthDTO{SPF: h.SPF, DMARC: h.DMARC, MX: h.MX}
}

// toSMTPDTO builds the smtp block, safely handling nil SMTP evidence (short
// circuits). It never fabricates accepted/rejected evidence.
func toSMTPDTO(a verification.Assessment) smtpDTO {
	dto := smtpDTO{
		SMTPAttempted:   a.SMTPAttempted,
		SMTPCheckReason: string(a.SMTPCheckReason),
	}
	if a.SMTP != nil {
		dto.HostExists = a.SMTP.HostExists
		dto.Deliverable = a.SMTP.Deliverable
		dto.CatchAll = a.SMTP.CatchAll
		dto.CatchAllResult = a.SMTP.CatchAllResult
		dto.FullInbox = a.SMTP.FullInbox
		dto.Disabled = a.SMTP.Disabled
		dto.RecipientResult = a.SMTP.RecipientResult
		dto.RecipientReason = a.SMTP.RecipientReason
		dto.SMTPCode = a.SMTP.SMTPCode
	}
	return dto
}

func toLegacyResponse(a verification.Assessment) legacyResponseDTO {
	res := a.Result
	if res == nil {
		// Defensive: never marshal a nil embedded pointer.
		res = &emailverifier.Result{}
	}
	return legacyResponseDTO{
		Result:              res,
		Status:              string(a.Status),
		ReasonCode:          string(a.ReasonCode),
		Retryable:           a.Retryable,
		Confidence:          a.Confidence,
		DeliverabilityScore: a.DeliverabilityScore,
		CheckedAt:           formatCheckedAt(a.CheckedAt),
		SMTPAttempted:       a.SMTPAttempted,
		SMTPCheckReason:     string(a.SMTPCheckReason),
		Source:              a.Source,
		Error:               toAPIError(a.Error),
	}
}

func toAPIError(e *verification.ErrorInfo) *apiError {
	if e == nil {
		return nil
	}
	return &apiError{Code: e.Code, Message: e.Message}
}

func formatCheckedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
