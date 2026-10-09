package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jakapobrs-oss/hospital-middleware/internal/config"
)

const validSecret = "0123456789abcdef0123456789abcdef"

// envFrom returns a getenv function backed by a map.
func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadFrom_AppliesDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(envFrom(map[string]string{
		"DATABASE_URL": "postgres://user:pass@db:5432/app",
		"JWT_SECRET":   validSecret,
	}))

	require.NoError(t, err)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, time.Hour, cfg.JWTTTL)
	assert.Equal(t, 3*time.Second, cfg.HISTimeout)
	assert.Empty(t, cfg.HISBaseURLs)
	assert.True(t, cfg.RunMigrations)
	assert.False(t, cfg.SeedDemoData)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
}

func TestLoadFrom_ReadsEveryVariable(t *testing.T) {
	cfg, err := config.LoadFrom(envFrom(map[string]string{
		"APP_PORT":       "9000",
		"DATABASE_URL":   "postgres://user:pass@db:5432/app",
		"JWT_SECRET":     validSecret,
		"JWT_TTL":        "30m",
		"HIS_BASE_URLS":  " Hospital-A = http://his-a:8081/ , hospital-b=https://his-b.example.com",
		"HIS_TIMEOUT":    "500ms",
		"RUN_MIGRATIONS": "false",
		"SEED_DEMO_DATA": "true",
		"LOG_LEVEL":      "debug",
	}))

	require.NoError(t, err)
	assert.Equal(t, "9000", cfg.Port)
	assert.Equal(t, 30*time.Minute, cfg.JWTTTL)
	assert.Equal(t, 500*time.Millisecond, cfg.HISTimeout)
	assert.Equal(t, map[string]string{
		"hospital-a": "http://his-a:8081",
		"hospital-b": "https://his-b.example.com",
	}, cfg.HISBaseURLs)
	assert.False(t, cfg.RunMigrations)
	assert.True(t, cfg.SeedDemoData)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
}

func TestLoadFrom_RejectsHISBaseURLWithoutHTTPScheme(t *testing.T) {
	_, err := config.LoadFrom(envFrom(map[string]string{
		"DATABASE_URL":  "postgres://user:pass@db:5432/app",
		"JWT_SECRET":    validSecret,
		"HIS_BASE_URLS": "hospital-a=ftp://his-a",
	}))

	assert.ErrorContains(t, err, "HIS_BASE_URLS")
}

func TestConfig_Warnings(t *testing.T) {
	sampleConfig := config.Config{JWTSecret: "change-me-to-a-long-random-secret-of-at-least-32-characters"}
	assert.Len(t, sampleConfig.Warnings(), 2, "sample JWT secret and open staff registration")

	hardenedConfig := config.Config{JWTSecret: validSecret, StaffRegistrationKey: "registration-key"}
	assert.Empty(t, hardenedConfig.Warnings())
}

func TestLoadFrom_ReadsStaffRegistrationKey(t *testing.T) {
	cfg, err := config.LoadFrom(envFrom(map[string]string{
		"DATABASE_URL":           "postgres://user:pass@db:5432/app",
		"JWT_SECRET":             validSecret,
		"STAFF_REGISTRATION_KEY": "registration-key",
	}))

	require.NoError(t, err)
	assert.Equal(t, "registration-key", cfg.StaffRegistrationKey)
}

func TestLoadFrom_ReportsEveryProblemAtOnce(t *testing.T) {
	_, err := config.LoadFrom(envFrom(map[string]string{
		"APP_PORT":       "eighty",
		"JWT_SECRET":     "too-short",
		"JWT_TTL":        "-5m",
		"HIS_TIMEOUT":    "soon",
		"HIS_BASE_URLS":  "hospital-a",
		"RUN_MIGRATIONS": "maybe",
		"SEED_DEMO_DATA": "perhaps",
		"LOG_LEVEL":      "loud",
	}))

	require.Error(t, err)
	for _, expectedProblem := range []string{
		"APP_PORT",
		"DATABASE_URL is required",
		"JWT_SECRET must be at least 32 characters",
		"JWT_TTL",
		"HIS_TIMEOUT",
		"HIS_BASE_URLS",
		"RUN_MIGRATIONS",
		"SEED_DEMO_DATA",
		"LOG_LEVEL",
	} {
		assert.Contains(t, err.Error(), expectedProblem)
	}
}
