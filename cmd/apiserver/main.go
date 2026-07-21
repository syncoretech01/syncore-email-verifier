package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	emailverifier "github.com/AfterShip/email-verifier"
	"github.com/AfterShip/email-verifier/internal/config"
	"github.com/AfterShip/email-verifier/internal/feedback"
	"github.com/AfterShip/email-verifier/internal/jobs"
	"github.com/AfterShip/email-verifier/internal/metrics"
	"github.com/AfterShip/email-verifier/internal/quota"
	"github.com/AfterShip/email-verifier/internal/ratelimit"
	"github.com/AfterShip/email-verifier/internal/store"
	"github.com/AfterShip/email-verifier/internal/suppression"
	"github.com/AfterShip/email-verifier/internal/verification"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Fail before binding the server; the message names the offending
		// variable and expected format without exposing secrets.
		log.Fatalf("configuration error: %v", err)
	}

	// Per-domain feedback/reputation store (fed by POST /v1/feedback); its priors
	// feed back into the deliverability score.
	feedbackStore := feedback.New()

	// One reusable verifier and one reusable verification service.
	engine := buildVerifier(cfg)
	suppressList := suppression.NewFromList(cfg.SuppressEmails)
	svc := verification.NewService(
		engine,
		verification.WithSMTPEnabled(cfg.SMTPEnabled),
		verification.WithDomainHealth(cfg.DomainHealth),
		verification.WithSuppressionCheck(suppressList.Contains),
		verification.WithReputation(func(domain string) (verification.DomainReputationEvidence, bool) {
			rep, ok := feedbackStore.Domain(domain)
			if !ok {
				return verification.DomainReputationEvidence{}, false
			}
			return verification.DomainReputationEvidence{
				Delivered:  rep.Delivered,
				Bounced:    rep.Bounced,
				Complained: rep.Complained,
				BounceRate: rep.BounceRate(),
			}, true
		}),
		verification.WithClock(func() time.Time { return time.Now().UTC() }),
	)

	// Structured logs + a dependency-free metrics registry.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	registry := metrics.New()

	// Optional per-client rate limiter and daily quota.
	var limiter *ratelimit.Limiter
	if cfg.RateLimitPerMinute > 0 {
		limiter = ratelimit.New(int(cfg.RateLimitPerMinute))
	}
	var dailyQuota *quota.Quota
	if cfg.DailyQuota > 0 {
		dailyQuota = quota.New(int(cfg.DailyQuota))
	}

	// Store backend (in-memory or Postgres) shared by the result cache and the
	// idempotency store.
	cacheStore, idemStore, readyFn, closeStore, err := buildStores(context.Background(), cfg)
	if err != nil {
		log.Fatalf("store initialization error: %v", err)
	}
	defer closeStore()

	// Optional result cache: enabled only when a positive TTL is set. The handler
	// depends on the VerificationService interface, so the cache decorator drops
	// in transparently.
	var vs VerificationService = svc
	if cfg.CacheTTL > 0 {
		vs = verification.NewCachingVerifier(svc, cacheStore, cfg.CacheTTL, cfg.CacheTTLUnknown)
		log.Printf("result cache enabled (store=%s, ttl=%s, unknown_ttl=%s)", cfg.Store, cfg.CacheTTL, cfg.CacheTTLUnknown)
	}

	// Async batch job manager (in-memory). Uses the same (possibly cached)
	// verification service and an optional signed-webhook sender.
	var webhook *jobs.Webhook
	if cfg.WebhookSigningKey != "" {
		webhook = jobs.NewWebhook(cfg.WebhookSigningKey, nil)
	}
	jobsMgr := jobs.NewManager(vs, jobs.Config{
		Workers:      int(cfg.Workers),
		RetryMax:     int(cfg.RetryMaxAttempts),
		RetryBackoff: cfg.RetryBackoff,
		Webhook:      webhook,
	})
	jobsMgr.Start()
	defer jobsMgr.Stop()

	handlers := newHandlers(handlerOpts{
		svc:          vs,
		maxBodyBytes: cfg.MaxBodyBytes,
		batch: batchConfig{
			maxItems:     int(cfg.BatchMaxItems),
			concurrency:  int(cfg.BatchConcurrency),
			maxBodyBytes: cfg.BatchMaxBodyBytes,
		},
		idempotency:        idemStore,
		jobs:               jobsMgr,
		asyncBatchMaxItems: int(cfg.AsyncBatchMaxItems),
		metrics:            registry,
		logger:             logger,
		ready:              readyFn,
		rateLimiter:        limiter,
		quota:              dailyQuota,
		apiKeyHashes:       apiKeyHashes(cfg.APIKeys),
		erase: func(ctx context.Context, email string) error {
			return cacheStore.Delete(ctx, strings.ToLower(strings.TrimSpace(email)))
		},
		feedbackStore: feedbackStore,
		feedbackKey:   []byte(cfg.FeedbackSigningKey),
	})
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

// apiKeyHashes maps each configured API key to its sha256 hex digest -> client
// name. Entries are "name:key" or a bare "key" (name defaults to "apikey").
// Keys are never stored in plaintext beyond config load.
func apiKeyHashes(entries []string) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		name, key := "apikey", e
		if i := strings.IndexByte(e, ':'); i > 0 {
			name, key = e[:i], e[i+1:]
		}
		if key == "" {
			continue
		}
		sum := sha256.Sum256([]byte(key))
		m[hex.EncodeToString(sum[:])] = name
	}
	return m
}

// buildStores builds the store backend for the result cache and idempotency
// store. For Postgres both share one connection pool (and table, via distinct
// key namespaces); for memory each gets its own bounded map. closeFn releases
// any resources (the pool) and is safe to defer.
func buildStores(ctx context.Context, cfg *config.Config) (cache, idem store.Store[verification.Assessment], ready func(context.Context) error, closeFn func(), err error) {
	switch cfg.Store {
	case "postgres":
		pool, err := store.NewPool(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, nil, func() {}, err
		}
		pg, err := store.NewPostgres[verification.Assessment](ctx, pool)
		if err != nil {
			pool.Close()
			return nil, nil, nil, func() {}, err
		}
		readyFn := func(ctx context.Context) error { return pool.Ping(ctx) }
		return pg, pg, readyFn, pool.Close, nil
	default:
		max := int(cfg.CacheMaxEntries)
		return store.NewMemory[verification.Assessment](max),
			store.NewMemory[verification.Assessment](max),
			func(context.Context) error { return nil },
			func() {}, nil
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
