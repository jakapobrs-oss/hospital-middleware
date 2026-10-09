// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	minJWTSecretLength = 32
	maxTCPPort         = 65535

	// placeholderSecretMarker appears in the sample secrets of .env.example.
	placeholderSecretMarker = "change-me"
)

// Config holds every runtime setting of the API service.
type Config struct {
	Port          string
	DatabaseURL   string
	JWTSecret     string
	JWTTTL        time.Duration
	HISBaseURLs   map[string]string // hospital code -> base URL of that hospital's HIS
	HISTimeout    time.Duration
	RunMigrations bool
	SeedDemoData  bool
	LogLevel      slog.Level
	// StaffRegistrationKey, when set, must accompany every /staff/create request. Empty keeps the endpoint open.
	StaffRegistrationKey string
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads the configuration through getenv so tests can supply their own values.
func LoadFrom(getenv func(string) string) (Config, error) {
	cfg := Config{
		Port:                 valueOrDefault(getenv("APP_PORT"), "8080"),
		DatabaseURL:          getenv("DATABASE_URL"),
		JWTSecret:            getenv("JWT_SECRET"),
		StaffRegistrationKey: getenv("STAFF_REGISTRATION_KEY"),
	}
	var problems []error

	if cfg.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required"))
	}
	if port, err := strconv.Atoi(cfg.Port); err != nil || port < 1 || port > maxTCPPort {
		problems = append(problems, fmt.Errorf("APP_PORT must be a number between 1 and %d", maxTCPPort))
	}
	if len(cfg.JWTSecret) < minJWTSecretLength {
		problems = append(problems, fmt.Errorf("JWT_SECRET must be at least %d characters", minJWTSecretLength))
	}

	var err error
	if cfg.JWTTTL, err = parseDuration(getenv("JWT_TTL"), time.Hour); err != nil {
		problems = append(problems, fmt.Errorf("JWT_TTL: %w", err))
	}
	if cfg.HISTimeout, err = parseDuration(getenv("HIS_TIMEOUT"), 3*time.Second); err != nil {
		problems = append(problems, fmt.Errorf("HIS_TIMEOUT: %w", err))
	}
	if cfg.HISBaseURLs, err = parseHISBaseURLs(getenv("HIS_BASE_URLS")); err != nil {
		problems = append(problems, fmt.Errorf("HIS_BASE_URLS: %w", err))
	}
	if cfg.RunMigrations, err = parseBool(getenv("RUN_MIGRATIONS"), true); err != nil {
		problems = append(problems, fmt.Errorf("RUN_MIGRATIONS: %w", err))
	}
	if cfg.SeedDemoData, err = parseBool(getenv("SEED_DEMO_DATA"), false); err != nil {
		problems = append(problems, fmt.Errorf("SEED_DEMO_DATA: %w", err))
	}
	if err = cfg.LogLevel.UnmarshalText([]byte(valueOrDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		problems = append(problems, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	if len(problems) > 0 {
		return Config{}, errors.Join(problems...)
	}
	return cfg, nil
}

// Warnings lists settings that work but are unsafe outside a local demo. The service logs them at start-up
// instead of refusing to start, so `docker compose up` with the sample .env still works for reviewers.
func (cfg Config) Warnings() []string {
	var warnings []string
	if strings.Contains(cfg.JWTSecret, placeholderSecretMarker) {
		warnings = append(warnings, "JWT_SECRET is the sample value from .env.example; anyone who knows it can forge access tokens")
	}
	if cfg.StaffRegistrationKey == "" {
		warnings = append(warnings, "STAFF_REGISTRATION_KEY is empty, so anyone can create a staff account for any hospital")
	}
	return warnings
}

// parseHISBaseURLs parses "hospital-a=http://his-a:8081,hospital-b=https://his-b.example.com".
func parseHISBaseURLs(raw string) (map[string]string, error) {
	baseURLs := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		hospitalCode, baseURL, found := strings.Cut(entry, "=")
		hospitalCode = strings.ToLower(strings.TrimSpace(hospitalCode))
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if !found || hospitalCode == "" || !isHTTPURL(baseURL) {
			return nil, fmt.Errorf("entry %q must look like hospital-code=http://host:port", entry)
		}
		baseURLs[hospitalCode] = baseURL
	}
	return baseURLs, nil
}

func isHTTPURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseDuration(raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New("must be positive")
	}
	return duration, nil
}

func parseBool(raw string, fallback bool) (bool, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}
