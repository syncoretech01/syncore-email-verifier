// Package config loads and validates the service configuration from the process
// environment. It reads process environment variables only; it does not read or
// load any .env file and adds no dotenv dependency.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	emailverifier "github.com/AfterShip/email-verifier"
)

// Environment variable names.
const (
	EnvBindAddr             = "SYNCORE_VERIFIER_BIND_ADDR"
	EnvSMTPEnabled          = "SYNCORE_VERIFIER_SMTP_ENABLED"
	EnvFromEmail            = "SYNCORE_VERIFIER_FROM_EMAIL"
	EnvHelloName            = "SYNCORE_VERIFIER_HELLO_NAME"
	EnvConnectTimeout       = "SYNCORE_VERIFIER_CONNECT_TIMEOUT"
	EnvOperationTimeout     = "SYNCORE_VERIFIER_OPERATION_TIMEOUT"
	EnvDisposableAutoUpdate = "SYNCORE_VERIFIER_DISPOSABLE_AUTOUPDATE"
	EnvDomainSuggest        = "SYNCORE_VERIFIER_DOMAIN_SUGGEST"
	EnvMaxBodyBytes         = "SYNCORE_VERIFIER_MAX_BODY_BYTES"
	EnvAuthToken            = "SYNCORE_VERIFIER_AUTH_TOKEN"
	EnvCacheTTL             = "SYNCORE_VERIFIER_CACHE_TTL"
	EnvCacheTTLUnknown      = "SYNCORE_VERIFIER_CACHE_TTL_UNKNOWN"
	EnvMXCacheTTL           = "SYNCORE_VERIFIER_MX_CACHE_TTL"
	EnvPurgeInterval        = "SYNCORE_VERIFIER_PURGE_INTERVAL"
	EnvCacheMaxEntries      = "SYNCORE_VERIFIER_CACHE_MAX_ENTRIES"
	EnvBatchMaxItems        = "SYNCORE_VERIFIER_BATCH_MAX_ITEMS"
	EnvBatchConcurrency     = "SYNCORE_VERIFIER_BATCH_CONCURRENCY"
	EnvBatchMaxBodyBytes    = "SYNCORE_VERIFIER_BATCH_MAX_BODY_BYTES"
	EnvDomainHealth         = "SYNCORE_VERIFIER_DOMAIN_HEALTH"
	EnvGravatarCheck        = "SYNCORE_VERIFIER_GRAVATAR_CHECK"
	EnvDNSBLCheck           = "SYNCORE_VERIFIER_DNSBL_CHECK"
	EnvDevConsole           = "SYNCORE_VERIFIER_DEV_CONSOLE"
	EnvStore                = "SYNCORE_VERIFIER_STORE"
	EnvDatabaseURL          = "SYNCORE_VERIFIER_DATABASE_URL"
	EnvWorkers              = "SYNCORE_VERIFIER_WORKERS"
	EnvAsyncBatchMaxItems   = "SYNCORE_VERIFIER_ASYNC_BATCH_MAX_ITEMS"
	EnvRetryMaxAttempts     = "SYNCORE_VERIFIER_RETRY_MAX_ATTEMPTS"
	EnvRetryBackoff         = "SYNCORE_VERIFIER_RETRY_BACKOFF"
	EnvWebhookSigningKey    = "SYNCORE_VERIFIER_WEBHOOK_SIGNING_KEY"
	EnvRateLimitPerMinute   = "SYNCORE_VERIFIER_RATE_LIMIT_PER_MINUTE"
	EnvAPIKeys              = "SYNCORE_VERIFIER_API_KEYS"
	EnvSuppressEmails       = "SYNCORE_VERIFIER_SUPPRESS_EMAILS"
	EnvFeedbackSigningKey   = "SYNCORE_VERIFIER_FEEDBACK_SIGNING_KEY"
	EnvFeedbackAdapterToken = "SYNCORE_VERIFIER_FEEDBACK_ADAPTER_TOKEN"
	EnvDailyQuota           = "SYNCORE_VERIFIER_DAILY_QUOTA"
)

// Config is the validated runtime configuration.
type Config struct {
	BindAddr             string
	SMTPEnabled          bool
	FromEmail            string
	HelloName            string
	ConnectTimeout       time.Duration
	OperationTimeout     time.Duration
	DisposableAutoUpdate bool
	DomainSuggest        bool
	MaxBodyBytes         int64
	// AuthToken, when non-empty, requires callers to present
	// "Authorization: Bearer <token>" on every verification endpoint. Empty
	// disables auth (only safe on a loopback bind — see validateBindSecurity).
	AuthToken string
	// CacheTTL is the lifetime of a cached terminal (valid/invalid) result.
	// Zero disables the result cache entirely (default).
	CacheTTL time.Duration
	// CacheTTLUnknown is the lifetime of a cached retryable (unknown) result.
	// Zero lets the cache derive min(CacheTTL, 1m).
	CacheTTLUnknown time.Duration
	// MXCacheTTL is the lifetime of a per-domain MX resolution cache entry, so
	// addresses sharing a domain reuse one lookup. Zero disables it.
	MXCacheTTL time.Duration
	// PurgeInterval is how often the background sweeper drops expired in-memory
	// cache entries. Zero disables the sweeper (entries still expire on access).
	PurgeInterval time.Duration
	// CacheMaxEntries bounds the in-memory result cache.
	CacheMaxEntries int64
	// BatchMaxItems caps the number of emails a single batch request may carry.
	BatchMaxItems int64
	// BatchConcurrency bounds the worker pool that processes a batch, so batches
	// never stampede mail providers.
	BatchConcurrency int64
	// BatchMaxBodyBytes caps the batch request body (larger than a single POST).
	BatchMaxBodyBytes int64
	// DomainHealth enables free SPF/DMARC/MX domain-health lookups (off by default).
	DomainHealth bool
	// GravatarCheck enables a per-address Gravatar lookup (an external HTTP call)
	// that adds engagement evidence and a small score bonus (off by default).
	GravatarCheck bool
	// DNSBLCheck enables a domain-blocklist (DNSBL, e.g. Spamhaus DBL) lookup that
	// adds `blocklisted` evidence and caps the score for listed domains (off by
	// default; adds one external DNS lookup per verification).
	DNSBLCheck bool
	// DevConsole serves a small same-origin HTML console at GET / for manually
	// trying verifications from a browser (off by default). Intended for local
	// use; when auth is enabled the page load requires a credential like any
	// other route, so the console's token field must be filled.
	DevConsole bool
	// Store selects the backend for the result cache and idempotency store:
	// "memory" (default) or "postgres".
	Store string
	// DatabaseURL is the Postgres connection string; required when Store=postgres.
	DatabaseURL string
	// Workers is the async-batch worker-pool size.
	Workers int64
	// AsyncBatchMaxItems caps emails in a POST /batches submission.
	AsyncBatchMaxItems int64
	// RetryMaxAttempts is the max retries for a retryable async-batch item (0 = off).
	RetryMaxAttempts int64
	// RetryBackoff is the base backoff between async-batch retries (0 = none).
	RetryBackoff time.Duration
	// WebhookSigningKey signs async-batch completion webhooks. Empty disables signing.
	WebhookSigningKey string
	// RateLimitPerMinute limits requests per client (bearer token or IP) per
	// minute; 0 disables rate limiting.
	RateLimitPerMinute int64
	// APIKeys are additional accepted credentials, each an optional "name:key"
	// (or bare "key"). Any valid API key authenticates like the bearer token.
	APIKeys []string
	// SuppressEmails seeds the do-not-verify list; suppressed addresses skip all
	// network checks and return suppressed=true.
	SuppressEmails []string
	// FeedbackSigningKey enables the HMAC-signed feedback ingestion endpoint.
	// Empty disables POST /v1/feedback.
	FeedbackSigningKey string
	// FeedbackAdapterToken is a shared secret gating the provider-specific
	// ingestion adapters (POST /v1/feedback/ses and /v1/feedback/smartlead).
	// Empty disables those endpoints.
	FeedbackAdapterToken string
	// DailyQuota caps requests per client per UTC day; 0 disables it.
	DailyQuota int64
}

// Load reads configuration from the process environment and validates it.
func Load() (*Config, error) {
	return loadFrom(os.LookupEnv)
}

// loadFrom builds a Config from an arbitrary lookup function. It is the testable
// core of Load and never mutates global state.
func loadFrom(lookup func(string) (string, bool)) (*Config, error) {
	cfg := &Config{
		BindAddr:  get(lookup, EnvBindAddr, "127.0.0.1:8080"),
		FromEmail: get(lookup, EnvFromEmail, "hello@syncoretech.com"),
		HelloName: get(lookup, EnvHelloName, "syncoretech.com"),
		AuthToken: get(lookup, EnvAuthToken, ""),
		APIKeys:   parseList(get(lookup, EnvAPIKeys, "")),
	}

	if err := validateBindAddr(cfg.BindAddr); err != nil {
		return nil, err
	}
	if err := validateBindSecurity(cfg.BindAddr, cfg.AuthToken, len(cfg.APIKeys) > 0); err != nil {
		return nil, err
	}

	var err error
	if cfg.SMTPEnabled, err = parseBool(lookup, EnvSMTPEnabled, true); err != nil {
		return nil, err
	}
	if cfg.DisposableAutoUpdate, err = parseBool(lookup, EnvDisposableAutoUpdate, false); err != nil {
		return nil, err
	}
	if cfg.DomainSuggest, err = parseBool(lookup, EnvDomainSuggest, true); err != nil {
		return nil, err
	}
	if cfg.ConnectTimeout, err = parseDuration(lookup, EnvConnectTimeout, 10*time.Second); err != nil {
		return nil, err
	}
	if cfg.OperationTimeout, err = parseDuration(lookup, EnvOperationTimeout, 10*time.Second); err != nil {
		return nil, err
	}
	if cfg.MaxBodyBytes, err = parsePositiveInt(lookup, EnvMaxBodyBytes, 4096); err != nil {
		return nil, err
	}
	if cfg.CacheTTL, err = parseOptionalDuration(lookup, EnvCacheTTL, 0); err != nil {
		return nil, err
	}
	if cfg.MXCacheTTL, err = parseOptionalDuration(lookup, EnvMXCacheTTL, 0); err != nil {
		return nil, err
	}
	if cfg.PurgeInterval, err = parseOptionalDuration(lookup, EnvPurgeInterval, 0); err != nil {
		return nil, err
	}
	if cfg.CacheTTLUnknown, err = parseOptionalDuration(lookup, EnvCacheTTLUnknown, 0); err != nil {
		return nil, err
	}
	if cfg.CacheMaxEntries, err = parsePositiveInt(lookup, EnvCacheMaxEntries, 10000); err != nil {
		return nil, err
	}
	if cfg.BatchMaxItems, err = parsePositiveInt(lookup, EnvBatchMaxItems, 100); err != nil {
		return nil, err
	}
	if cfg.BatchConcurrency, err = parsePositiveInt(lookup, EnvBatchConcurrency, 10); err != nil {
		return nil, err
	}
	if cfg.BatchMaxBodyBytes, err = parsePositiveInt(lookup, EnvBatchMaxBodyBytes, 65536); err != nil {
		return nil, err
	}
	if cfg.GravatarCheck, err = parseBool(lookup, EnvGravatarCheck, false); err != nil {
		return nil, err
	}
	if cfg.DNSBLCheck, err = parseBool(lookup, EnvDNSBLCheck, false); err != nil {
		return nil, err
	}
	if cfg.DevConsole, err = parseBool(lookup, EnvDevConsole, false); err != nil {
		return nil, err
	}
	if cfg.DomainHealth, err = parseBool(lookup, EnvDomainHealth, false); err != nil {
		return nil, err
	}

	cfg.Store = get(lookup, EnvStore, "memory")
	if cfg.Store != "memory" && cfg.Store != "postgres" {
		return nil, fmt.Errorf("%s: must be 'memory' or 'postgres'", EnvStore)
	}
	cfg.DatabaseURL = get(lookup, EnvDatabaseURL, "")
	if cfg.Store == "postgres" && cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%s: required when %s=postgres", EnvDatabaseURL, EnvStore)
	}
	if cfg.Workers, err = parsePositiveInt(lookup, EnvWorkers, 4); err != nil {
		return nil, err
	}
	if cfg.AsyncBatchMaxItems, err = parsePositiveInt(lookup, EnvAsyncBatchMaxItems, 10000); err != nil {
		return nil, err
	}
	if cfg.RetryMaxAttempts, err = parseNonNegativeInt(lookup, EnvRetryMaxAttempts, 0); err != nil {
		return nil, err
	}
	if cfg.RetryBackoff, err = parseOptionalDuration(lookup, EnvRetryBackoff, 0); err != nil {
		return nil, err
	}
	cfg.WebhookSigningKey = get(lookup, EnvWebhookSigningKey, "")
	if cfg.RateLimitPerMinute, err = parseNonNegativeInt(lookup, EnvRateLimitPerMinute, 0); err != nil {
		return nil, err
	}
	cfg.SuppressEmails = parseList(get(lookup, EnvSuppressEmails, ""))
	cfg.FeedbackSigningKey = get(lookup, EnvFeedbackSigningKey, "")
	cfg.FeedbackAdapterToken = get(lookup, EnvFeedbackAdapterToken, "")
	if cfg.DailyQuota, err = parseNonNegativeInt(lookup, EnvDailyQuota, 0); err != nil {
		return nil, err
	}

	// FROM_EMAIL and HELLO_NAME are only used for SMTP, so they are validated
	// only when SMTP is enabled; otherwise they must not block startup.
	if cfg.SMTPEnabled {
		if !emailverifier.IsAddressValid(cfg.FromEmail) {
			return nil, fmt.Errorf("%s: must be a valid email address when %s=true", EnvFromEmail, EnvSMTPEnabled)
		}
		if cfg.HelloName == "" {
			return nil, fmt.Errorf("%s: must be non-empty when %s=true", EnvHelloName, EnvSMTPEnabled)
		}
		if hasWhitespaceOrControl(cfg.HelloName) {
			return nil, fmt.Errorf("%s: must not contain whitespace or control characters when %s=true", EnvHelloName, EnvSMTPEnabled)
		}
	}

	return cfg, nil
}

func get(lookup func(string) (string, bool), key, def string) string {
	if v, ok := lookup(key); ok {
		return v
	}
	return def
}

func parseBool(lookup func(string) (string, bool), key string, def bool) (bool, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: must be a boolean (true or false)", key)
	}
	return b, nil
}

func parseDuration(lookup func(string) (string, bool), key string, def time.Duration) (time.Duration, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: must be a Go duration (e.g. 10s)", key)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: must be greater than zero", key)
	}
	return d, nil
}

// parseOptionalDuration parses a Go duration that may be zero to mean "disabled".
// Unset returns def; a negative value is rejected.
func parseOptionalDuration(lookup func(string) (string, bool), key string, def time.Duration) (time.Duration, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: must be a Go duration (e.g. 10m)", key)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s: must not be negative", key)
	}
	return d, nil
}

// parseNonNegativeInt parses an integer that may be zero.
func parseNonNegativeInt(lookup func(string) (string, bool), key string, def int64) (int64, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: must be an integer", key)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s: must not be negative", key)
	}
	return n, nil
}

func parsePositiveInt(lookup func(string) (string, bool), key string, def int64) (int64, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: must be an integer", key)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: must be a positive integer", key)
	}
	return n, nil
}

func validateBindAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s: must be host:port", EnvBindAddr)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("%s: port must be numeric", EnvBindAddr)
	}
	if p < 0 || p > 65535 {
		return fmt.Errorf("%s: port must be within 0-65535", EnvBindAddr)
	}
	return nil
}

// validateBindSecurity refuses to expose the service on a non-loopback address
// without an auth token set. Binding a public or LAN interface with no bearer
// token would let anyone who can reach the host drive SMTP probes through it.
func validateBindSecurity(addr, token string, hasAPIKeys bool) error {
	if token != "" || hasAPIKeys {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Already validated by validateBindAddr; nothing more to check here.
		return nil
	}
	if isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%s: must be set when %s binds a non-loopback address (%q)", EnvAuthToken, EnvBindAddr, addr)
}

// isLoopbackHost reports whether the bind host is confined to the local machine.
// An empty host (bind all interfaces) or any non-"localhost" hostname is treated
// as non-loopback.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// parseList splits a comma-separated value into trimmed, non-empty entries.
func parseList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasWhitespaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
