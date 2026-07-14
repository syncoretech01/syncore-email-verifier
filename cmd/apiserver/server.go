package main

import (
	"log"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"

	"github.com/AfterShip/email-verifier/internal/config"
)

// newRouter wires the routes and cross-cutting handlers, returning the fully
// wrapped handler (panic recovery on the outside so it covers every route,
// including NotFound and MethodNotAllowed).
func newRouter(h *Handlers) http.Handler {
	router := httprouter.New()

	router.GET("/v1/:email/verification", h.handleGetVerification)
	router.POST("/v1/verifications", h.handleVerifications)
	router.GET("/health", h.handleHealth)

	router.NotFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	// httprouter sets the Allow header before invoking MethodNotAllowed, so the
	// 405 response carries the allowed methods.
	router.MethodNotAllowed = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	return recoverMiddleware(router)
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
		Handler:           newRouter(h),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeoutFor(cfg),
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
}

// writeTimeoutFor derives a WriteTimeout that will not truncate a legitimate
// verification. A single slow MX can take up to ConnectTimeout + OperationTimeout
// (they are configurable), so the write deadline is that sum plus 15s of
// headroom, and never less than 35s.
func writeTimeoutFor(cfg *config.Config) time.Duration {
	const (
		headroom = 15 * time.Second
		floor    = 35 * time.Second
	)
	wt := cfg.ConnectTimeout + cfg.OperationTimeout + headroom
	if wt < floor {
		wt = floor
	}
	return wt
}
