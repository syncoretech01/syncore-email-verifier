package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/metrics"
)

// observeMiddleware records request metrics and emits a structured, PII-free
// access log with a per-request ID. It normalizes the path to a bounded route
// label (never logging the email embedded in the legacy GET path).
func observeMiddleware(reg *metrics.Registry, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := newRequestID()
		w.Header().Set("X-Request-ID", reqID)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		dur := time.Since(start)
		route := routeLabel(r.URL.Path)
		if reg != nil {
			reg.CounterInc("http_requests_total", "HTTP requests by route, method, and status code",
				map[string]string{"route": route, "method": r.Method, "code": strconv.Itoa(rec.status)})
			reg.ObserveLatency("http_request_duration_seconds", "HTTP request duration in seconds", dur)
		}
		if logger != nil {
			logger.Info("http_request",
				"request_id", reqID,
				"method", r.Method,
				"route", route,
				"status", rec.status,
				"duration_ms", dur.Milliseconds(),
			)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// routeLabel maps a path to a low-cardinality route, and — critically — never
// echoes the email in the legacy GET path.
func routeLabel(path string) string {
	switch {
	case path == "/health":
		return "/health"
	case path == "/ready":
		return "/ready"
	case path == "/metrics":
		return "/metrics"
	case path == "/v1/verifications":
		return "/v1/verifications"
	case path == "/v1/verifications:batch":
		return "/v1/verifications:batch"
	case path == "/batches":
		return "/batches"
	case strings.HasPrefix(path, "/batches/") && strings.HasSuffix(path, "/results"):
		return "/batches/:id/results"
	case strings.HasPrefix(path, "/batches/"):
		return "/batches/:id"
	case strings.HasPrefix(path, "/v1/") && strings.HasSuffix(path, "/verification"):
		return "/v1/:email/verification"
	default:
		return "other"
	}
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// handleMetrics exposes the Prometheus text exposition format.
func (h *Handlers) handleMetrics(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	if h.metrics == nil {
		writeError(w, http.StatusServiceUnavailable, "not_available", "metrics are not enabled")
		return
	}
	var b strings.Builder
	h.metrics.WriteText(&b)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}

// handleReady reports readiness, probing the store when a checker is configured.
func (h *Handlers) handleReady(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if h.ready != nil {
		if err := h.ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// recordVerification counts a completed verification by result status.
func (h *Handlers) recordVerification(status string) {
	if h.metrics != nil {
		h.metrics.CounterInc("verifications_total", "Verifications by result status",
			map[string]string{"status": status})
	}
}
