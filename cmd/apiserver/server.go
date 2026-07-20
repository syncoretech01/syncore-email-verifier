package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/config"
)

// newRouter wires the routes and cross-cutting handlers, returning the fully
// wrapped handler. Middleware order is outside-in: panic recovery wraps auth
// wraps the router, so recovery covers every route (including NotFound and
// MethodNotAllowed) and auth runs before routing.
func newRouter(h *Handlers, authToken string) http.Handler {
	router := httprouter.New()

	router.GET("/v1/:email/verification", h.handleGetVerification)
	router.POST("/v1/verifications", h.handleVerifications)
	router.POST("/v1/verifications:batch", h.handleVerificationsBatch)
	router.GET("/health", h.handleHealth)

	router.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	// httprouter sets the Allow header before invoking MethodNotAllowed, so the
	// 405 response carries the allowed methods.
	router.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	return recoverMiddleware(authMiddleware(authToken, router))
}

// healthPath is the one route that stays open when auth is enabled, so probes
// and load balancers can check liveness without a credential.
const healthPath = "/health"

// authMiddleware enforces a bearer token on every route except /health. When the
// token is empty, auth is disabled and the middleware is a pass-through (only
// reached on a loopback bind — see config.validateBindSecurity). The token
// comparison is constant-time to avoid leaking it via timing.
func authMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath {
			next.ServeHTTP(w, r)
			return
		}
		provided, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || subtle.ConstantTimeCompare([]byte(provided), expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the credential from an "Authorization: Bearer <token>"
// header. The scheme is matched case-insensitively per RFC 7235; the token is
// returned verbatim.
func bearerToken(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	return header[len(scheme):], true
}

// recoverMiddleware converts an unexpected panic into an HTTP 500 JSON response
// without leaking the panic value or a stack trace to the client.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// Safe internal diagnostic only: method + path, never the panic
				// value or stack, and never sent to the client.
				log.Printf("recovered panic while handling %s %s", r.Method, r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// newServer builds a hardened *http.Server from configuration.
func newServer(cfg *config.Config, h *Handlers) *http.Server {
	return &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           newRouter(h, cfg.AuthToken),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeoutFor(cfg),
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
}

// writeTimeoutFor derives a WriteTimeout that will not truncate a legitimate
// verification. A single slow MX can take up to ConnectTimeout + OperationTimeout
// (they are configurable). The batch endpoint runs at most
// ceil(BatchMaxItems / BatchConcurrency) sequential rounds of that worst case, so
// the write deadline must cover the larger of the two, plus 15s of headroom, and
// never less than 35s. The resulting batch bound is documented in deploy/ so the
// CRM can chunk its requests to fit.
func writeTimeoutFor(cfg *config.Config) time.Duration {
	const (
		headroom = 15 * time.Second
		floor    = 35 * time.Second
	)
	perItem := cfg.ConnectTimeout + cfg.OperationTimeout

	wt := perItem + headroom
	if batch := batchWorstCase(cfg) + headroom; batch > wt {
		wt = batch
	}
	if wt < floor {
		wt = floor
	}
	return wt
}

// batchWorstCase is the worst-case wall-clock for a full-capacity batch:
// ceil(BatchMaxItems / BatchConcurrency) rounds, each up to
// ConnectTimeout + OperationTimeout (concurrent MX within an item do not sum).
func batchWorstCase(cfg *config.Config) time.Duration {
	items := cfg.BatchMaxItems
	concurrency := cfg.BatchConcurrency
	if items <= 0 || concurrency <= 0 {
		return 0
	}
	rounds := (items + concurrency - 1) / concurrency // ceil division
	return time.Duration(rounds) * (cfg.ConnectTimeout + cfg.OperationTimeout)
}
