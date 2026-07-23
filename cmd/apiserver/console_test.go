package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// newConsoleServer builds the router with the dev console enabled.
func newConsoleServer(t *testing.T) http.Handler {
	t.Helper()
	return newRouter(newHandlers(handlerOpts{
		svc:        &stubService{assessment: acceptedAssessment()},
		batch:      defaultTestBatch,
		logger:     testLogger,
		devConsole: true,
	}), "")
}

func TestConsole_ServedWhenEnabled(t *testing.T) {
	rec := do(t, newConsoleServer(t), http.MethodGet, "/", "", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"))
	// The embedded page loaded and looks like the console.
	assert.Contains(t, rec.Body.String(), "/v1/verifications")
	assert.Contains(t, strings.ToLower(rec.Body.String()), "<!doctype html>")
}

func TestConsole_NotFoundWhenDisabled(t *testing.T) {
	// The default test server does not enable the console.
	rec := do(t, newTestServer(t, &stubService{assessment: acceptedAssessment()}, 0), http.MethodGet, "/", "", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestConsole_APICallStillReachableWithConsoleOn(t *testing.T) {
	// Enabling the console must not disturb the API routes.
	rec := do(t, newConsoleServer(t), http.MethodPost, "/v1/verifications", "application/json", `{"email":"person@example.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}
