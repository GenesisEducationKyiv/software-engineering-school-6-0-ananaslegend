package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateEnv unsets every variable Config reads and registers a cleanup
// that restores the prior value (or unsets again if absent).
// `t.Setenv(k, "")` is intentionally NOT used here because envconfig tries
// to parse the empty string for typed fields (Duration, int) before
// checking `required:"true"` — "DATABASE_URL missing" would be masked by
// "invalid duration ”".
func isolateEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"HTTP_ADDR", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
		"DATABASE_URL", "DB_MAX_CONNS", "REDIS_URL",
		"APP_BASE_URL", "CONFIRM_TOKEN_TTL",
		"LOG_LEVEL", "LOG_PRETTY",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM", "SMTP_TLS_POLICY",
		"RESEND_API_KEY", "RESEND_FROM",
		"GITHUB_TOKEN",
		"SCANNER_INTERVAL", "NOTIFIER_INTERVAL", "CONFIRMER_INTERVAL",
	}
	for _, k := range keys {
		if orig, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, orig) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(k) })
		}
		_ = os.Unsetenv(k)
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	isolateEnv(t)
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestLoad_AppliesDefaultsWhenOnlyRequiredSet(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x?sslmode=disable")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "http://localhost:8080", cfg.AppBaseURL)
	assert.Equal(t, 24*time.Hour, cfg.ConfirmTokenTTL)
	assert.Equal(t, int32(10), cfg.DBMaxConns)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "starttls", cfg.SMTPTLSPolicy)
	assert.Empty(t, cfg.RedisURL, "REDIS_URL must default to empty (silent fallback)")
	assert.Empty(t, cfg.GitHubToken, "GITHUB_TOKEN may be empty (WARN path lives elsewhere)")
}

func TestLoad_RedisURLOptional(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.RedisURL)
}

func TestLoad_InvalidDurationRejected(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("CONFIRM_TOKEN_TTL", "twenty-four-hours")
	_, err := Load()
	require.Error(t, err)
}
