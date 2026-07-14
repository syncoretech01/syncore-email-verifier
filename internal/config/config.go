// Package config loads and validates the service configuration from the process
// environment. It reads process environment variables only; it does not read or
// load any .env file and adds no dotenv dependency.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
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
	}

	if err := validateBindAddr(cfg.BindAddr); err != nil {
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

func hasWhitespaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
