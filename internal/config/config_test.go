package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lookupFrom returns a lookup function backed by a map, giving each test a fully
// isolated environment with no global state and no leakage between tests.
func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := loadFrom(lookupFrom(map[string]string{}))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", cfg.BindAddr)
	assert.True(t, cfg.SMTPEnabled)
	assert.Equal(t, "hello@syncoretech.com", cfg.FromEmail)
	assert.Equal(t, "syncoretech.com", cfg.HelloName)
	assert.Equal(t, 10*time.Second, cfg.ConnectTimeout)
	assert.Equal(t, 10*time.Second, cfg.OperationTimeout)
	assert.False(t, cfg.DisposableAutoUpdate)
	assert.True(t, cfg.DomainSuggest)
	assert.Equal(t, int64(4096), cfg.MaxBodyBytes)
	assert.Equal(t, "", cfg.AuthToken)
	assert.Equal(t, time.Duration(0), cfg.CacheTTL)
	assert.Equal(t, time.Duration(0), cfg.CacheTTLUnknown)
	assert.Equal(t, int64(10000), cfg.CacheMaxEntries)
	assert.Equal(t, int64(100), cfg.BatchMaxItems)
	assert.Equal(t, int64(10), cfg.BatchConcurrency)
	assert.Equal(t, int64(65536), cfg.BatchMaxBodyBytes)
	assert.False(t, cfg.DomainHealth)
	assert.Equal(t, "memory", cfg.Store)
	assert.Equal(t, "", cfg.DatabaseURL)
}

func TestLoad_EachOverride(t *testing.T) {
	env := map[string]string{
		EnvBindAddr:             "0.0.0.0:9000",
		EnvSMTPEnabled:          "false",
		EnvFromEmail:            "sender@example.org",
		EnvHelloName:            "mail.example.org",
		EnvConnectTimeout:       "3s",
		EnvOperationTimeout:     "7s",
		EnvDisposableAutoUpdate: "true",
		EnvDomainSuggest:        "false",
		EnvMaxBodyBytes:         "8192",
		// A non-loopback bind requires a token (see validateBindSecurity).
		EnvAuthToken:         "override-token",
		EnvCacheTTL:          "10m",
		EnvCacheTTLUnknown:   "30s",
		EnvCacheMaxEntries:   "500",
		EnvBatchMaxItems:     "50",
		EnvBatchConcurrency:  "5",
		EnvBatchMaxBodyBytes: "131072",
		EnvDomainHealth:      "true",
		EnvStore:             "postgres",
		EnvDatabaseURL:       "postgres://user:pass@localhost:5432/verifier",
	}
	cfg, err := loadFrom(lookupFrom(env))
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:9000", cfg.BindAddr)
	assert.False(t, cfg.SMTPEnabled)
	assert.Equal(t, "sender@example.org", cfg.FromEmail)
	assert.Equal(t, "mail.example.org", cfg.HelloName)
	assert.Equal(t, 3*time.Second, cfg.ConnectTimeout)
	assert.Equal(t, 7*time.Second, cfg.OperationTimeout)
	assert.True(t, cfg.DisposableAutoUpdate)
	assert.False(t, cfg.DomainSuggest)
	assert.Equal(t, int64(8192), cfg.MaxBodyBytes)
	assert.Equal(t, "override-token", cfg.AuthToken)
	assert.Equal(t, 10*time.Minute, cfg.CacheTTL)
	assert.Equal(t, 30*time.Second, cfg.CacheTTLUnknown)
	assert.Equal(t, int64(500), cfg.CacheMaxEntries)
	assert.Equal(t, int64(50), cfg.BatchMaxItems)
	assert.Equal(t, int64(5), cfg.BatchConcurrency)
	assert.Equal(t, int64(131072), cfg.BatchMaxBodyBytes)
	assert.True(t, cfg.DomainHealth)
	assert.Equal(t, "postgres", cfg.Store)
	assert.Equal(t, "postgres://user:pass@localhost:5432/verifier", cfg.DatabaseURL)
}

func TestLoad_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		// substring expected in the error, and the env var name it must mention.
		wantVar string
	}{
		{"invalid boolean", map[string]string{EnvSMTPEnabled: "yesplease"}, EnvSMTPEnabled},
		{"invalid duration", map[string]string{EnvConnectTimeout: "ten-seconds"}, EnvConnectTimeout},
		{"zero duration", map[string]string{EnvOperationTimeout: "0s"}, EnvOperationTimeout},
		{"negative duration", map[string]string{EnvConnectTimeout: "-5s"}, EnvConnectTimeout},
		{"malformed bind address", map[string]string{EnvBindAddr: "127.0.0.1"}, EnvBindAddr},
		{"non-numeric port", map[string]string{EnvBindAddr: "127.0.0.1:http"}, EnvBindAddr},
		{"out-of-range port", map[string]string{EnvBindAddr: "127.0.0.1:99999"}, EnvBindAddr},
		{"zero body limit", map[string]string{EnvMaxBodyBytes: "0"}, EnvMaxBodyBytes},
		{"negative body limit", map[string]string{EnvMaxBodyBytes: "-1"}, EnvMaxBodyBytes},
		{"non-integer body limit", map[string]string{EnvMaxBodyBytes: "big"}, EnvMaxBodyBytes},
		{"smtp enabled invalid from email", map[string]string{EnvSMTPEnabled: "true", EnvFromEmail: "not-an-email"}, EnvFromEmail},
		{"smtp enabled empty hello name", map[string]string{EnvSMTPEnabled: "true", EnvHelloName: ""}, EnvHelloName},
		{"smtp enabled whitespace hello name", map[string]string{EnvSMTPEnabled: "true", EnvHelloName: "bad host"}, EnvHelloName},
		{"smtp enabled control hello name", map[string]string{EnvSMTPEnabled: "true", EnvHelloName: "bad\x01host"}, EnvHelloName},
		{"non-loopback bind without token", map[string]string{EnvBindAddr: "0.0.0.0:9000"}, EnvAuthToken},
		{"all-interfaces bind without token", map[string]string{EnvBindAddr: ":9000"}, EnvAuthToken},
		{"lan bind without token", map[string]string{EnvBindAddr: "192.168.1.10:9000"}, EnvAuthToken},
		{"negative cache ttl", map[string]string{EnvCacheTTL: "-1m"}, EnvCacheTTL},
		{"invalid cache ttl", map[string]string{EnvCacheTTL: "tenminutes"}, EnvCacheTTL},
		{"invalid unknown cache ttl", map[string]string{EnvCacheTTLUnknown: "nope"}, EnvCacheTTLUnknown},
		{"zero cache max entries", map[string]string{EnvCacheMaxEntries: "0"}, EnvCacheMaxEntries},
		{"zero batch max items", map[string]string{EnvBatchMaxItems: "0"}, EnvBatchMaxItems},
		{"negative batch concurrency", map[string]string{EnvBatchConcurrency: "-1"}, EnvBatchConcurrency},
		{"non-integer batch body", map[string]string{EnvBatchMaxBodyBytes: "big"}, EnvBatchMaxBodyBytes},
		{"invalid domain health bool", map[string]string{EnvDomainHealth: "maybe"}, EnvDomainHealth},
		{"invalid store", map[string]string{EnvStore: "mysql"}, EnvStore},
		{"postgres without database url", map[string]string{EnvStore: "postgres"}, EnvDatabaseURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadFrom(lookupFrom(tc.env))
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantVar, "error must name the offending variable")
		})
	}
}

func TestLoad_SMTPDisabledIgnoresFromAndHello(t *testing.T) {
	// With SMTP disabled, invalid FROM_EMAIL and HELLO_NAME must NOT block startup.
	env := map[string]string{
		EnvSMTPEnabled: "false",
		EnvFromEmail:   "definitely not valid",
		EnvHelloName:   "has spaces and \x01 control",
	}
	cfg, err := loadFrom(lookupFrom(env))
	require.NoError(t, err)
	assert.False(t, cfg.SMTPEnabled)
}

func TestLoad_SMTPDisabledEmptyHelloNameOK(t *testing.T) {
	env := map[string]string{EnvSMTPEnabled: "false", EnvHelloName: ""}
	cfg, err := loadFrom(lookupFrom(env))
	require.NoError(t, err)
	assert.False(t, cfg.SMTPEnabled)
}

func TestLoad_NonLoopbackBindWithTokenOK(t *testing.T) {
	// A non-loopback bind is allowed once an auth token is set.
	for _, addr := range []string{"0.0.0.0:9000", ":9000", "192.168.1.10:9000"} {
		cfg, err := loadFrom(lookupFrom(map[string]string{
			EnvBindAddr:  addr,
			EnvAuthToken: "a-token",
		}))
		require.NoErrorf(t, err, "addr %q with token should load", addr)
		assert.Equal(t, addr, cfg.BindAddr)
	}
}

func TestLoad_CacheTTLZeroDisables(t *testing.T) {
	// An explicit "0s" is accepted and means the cache stays disabled.
	cfg, err := loadFrom(lookupFrom(map[string]string{EnvCacheTTL: "0s"}))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), cfg.CacheTTL)
}

func TestLoad_LoopbackBindNeedsNoToken(t *testing.T) {
	// Loopback binds are safe without a token.
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		cfg, err := loadFrom(lookupFrom(map[string]string{EnvBindAddr: addr}))
		require.NoErrorf(t, err, "loopback addr %q should load tokenless", addr)
		assert.Equal(t, "", cfg.AuthToken)
	}
}

// TestLoad_NoLeakageBetweenTests confirms the map-based lookup is fully isolated:
// two loads with different maps do not influence each other.
func TestLoad_NoLeakageBetweenTests(t *testing.T) {
	a, err := loadFrom(lookupFrom(map[string]string{EnvBindAddr: "127.0.0.1:1111"}))
	require.NoError(t, err)
	b, err := loadFrom(lookupFrom(map[string]string{}))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:1111", a.BindAddr)
	assert.Equal(t, "127.0.0.1:8080", b.BindAddr)
}
