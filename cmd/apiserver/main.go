package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/config"
	"github.com/AfterShip/email-verifier/internal/verification"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Fail before binding the server; the message names the offending
		// variable and expected format without exposing secrets.
		log.Fatalf("configuration error: %v", err)
	}

	// One reusable verifier and one reusable verification service.
	engine := buildVerifier(cfg)
	svc := verification.NewService(
		engine,
		verification.WithSMTPEnabled(cfg.SMTPEnabled),
		verification.WithClock(func() time.Time { return time.Now().UTC() }),
	)
	handlers := newHandlers(svc, cfg.MaxBodyBytes)
	server := newServer(cfg, handlers)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Syncore email verifier listening on http://%s", cfg.BindAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		// e.g. the bind address is unavailable. Exit non-zero from the main
		// goroutine (never log.Fatal from the serving goroutine).
		log.Printf("server error: %v", err)
		os.Exit(1)
	case <-ctx.Done():
		log.Println("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}

// buildVerifier constructs a single reusable verifier from configuration. SOCKS
// proxy support remains available via the engine's Proxy option; Phase 1C does
// not add a proxy environment variable.
func buildVerifier(cfg *config.Config) *emailverifier.Verifier {
	v := emailverifier.NewVerifier()

	if cfg.SMTPEnabled {
		v.EnableSMTPCheck()
	} else {
		v.DisableSMTPCheck()
	}

	v.FromEmail(cfg.FromEmail)
	v.HelloName(cfg.HelloName)
	v.ConnectTimeout(cfg.ConnectTimeout)
	v.OperationTimeout(cfg.OperationTimeout)

	if cfg.DomainSuggest {
		v.EnableDomainSuggest()
	} else {
		v.DisableDomainSuggest()
	}
	if cfg.DisposableAutoUpdate {
		v.EnableAutoUpdateDisposable()
	}

	return v
}
