package main

import (
	_ "embed"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

// consoleHTML is the optional browser console page, embedded at build time so the
// binary stays self-contained (no runtime asset path to configure).
//
//go:embed console.html
var consoleHTML []byte

// handleConsole serves the browser console: a single same-origin HTML page that
// posts to /v1/verifications from the client. The page carries no data itself
// and reaches only same-origin endpoints, so a strict CSP is applied. Enabled
// only when SYNCORE_VERIFIER_DEV_CONSOLE=true.
func (h *Handlers) handleConsole(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; connect-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(consoleHTML)
}
