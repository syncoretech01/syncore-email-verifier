package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/config"
	"github.com/AfterShip/email-verifier/internal/quota"
	"github.com/AfterShip/email-verifier/internal/ratelimit"
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
	// Async batch API lives under /batches: httprouter's legacy GET
	// /v1/:email/verification param forbids a static GET sibling under /v1/.
	router.POST("/batches", h.handleBatchSubmit)
	router.GET("/batches/:id", h.handleBatchStatus)
	router.GET("/batches/:id/results", h.handleBatchResults)
	router.POST("/v1/feedback", h.handleFeedback)
	router.POST("/v1/feedback/ses", h.handleFeedbackSES)
	router.POST("/v1/feedback/smartlead", h.handleFeedbackSmartlead)
	router.POST("/admin/erasure", h.handleErasure)
	router.GET("/health", h.handleHealth)
	router.GET("/ready", h.handleReady)
	router.GET("/metrics", h.handleMetrics)
	// Optional browser console (off by default). Served same-origin so its API
	// calls need no CORS; it still passes through auth/rate-limit like any route.
	if h.devConsole {
		router.GET("/", h.handleConsole)
	}

	router.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	// httprouter sets the Allow header before invoking MethodNotAllowed, so the
	// 405 response carries the allowed methods.
	router.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	// Outermost: observe (records + logs, sees the final status). Then panic
	// recovery, then auth, then rate-limit, then the router.
	auth := authenticator{token: authToken, keyHashes: h.apiKeyHashes}
	return observeMiddleware(h.metrics, h.logger,
		recoverMiddleware(authMiddleware(auth,
			rateLimitMiddleware(h.rateLimiter,
				quotaMiddleware(h.quota, router)))))
}

// quotaMiddleware enforces a per-client daily request cap (keyed like the rate
// limiter). Over quota -> 429. /health and /ready are exempt. Nil is a pass-through.
func quotaMiddleware(q *quota.Quota, next http.Handler) http.Handler {
	if q == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !q.Allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "quota_exceeded", "daily quota exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticator validates a presented credential against the global bearer token
// (constant-time) and/or a set of hashed API keys.
type authenticator struct {
	token     string
	keyHashes map[string]string // sha256(key) hex -> client name
}

func (a authenticator) enabled() bool { return a.token != "" || len(a.keyHashes) > 0 }

func (a authenticator) validate(cred string) bool {
	if a.token != "" && subtle.ConstantTimeCompare([]byte(cred), []byte(a.token)) == 1 {
		return true
	}
	if len(a.keyHashes) > 0 {
		sum := sha256.Sum256([]byte(cred))
		if _, ok := a.keyHashes[hex.EncodeToString(sum[:])]; ok {
			return true
		}
	}
	return false
}

// rateLimitMiddleware enforces a per-client token-bucket limit, keyed by the
// bearer token when present, else the remote IP. /health and /ready are exempt.
// A nil limiter is a pass-through.
func rateLimitMiddleware(limiter *ratelimit.Limiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.Allow(clientKey(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller for rate limiting: the bearer token if present
// (a stable per-key identity), otherwise the request's remote host.
func clientKey(r *http.Request) string {
	if tok, ok := bearerToken(r.Header.Get("Authorization")); ok && tok != "" {
		return "tok:" + tok
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// isAuthExempt reports whether a path stays open when auth is enabled, so probes
// and load balancers can check liveness/readiness without a credential. /metrics
// stays authenticated (it carries operational data).
func isAuthExempt(path string) bool {
	return path == "/health" || path == "/ready"
}

// authMiddleware enforces a valid bearer token or API key on every route except
// the exempt ones. When auth is disabled (no token and no keys) it is a
// pass-through (only reached on a loopback bind — see config.validateBindSecurity).
func authMiddleware(auth authenticator, next http.Handler) http.Handler {
	if !auth.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		cred, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !auth.validate(cred) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token or API key is required")
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
