package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
)

const (
	localDatabaseURL   = "postgres://business_drift:business_drift@localhost:5432/business_drift?sslmode=disable"
	localRedisURL      = "redis://localhost:6379/0"
	localEncryptionKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

// Config contains the settings the application needs to start.
type Config struct {
	Environment   string
	HTTPAddress   string
	DatabaseURL   string
	RedisURL      string
	EncryptionKey []byte
}

// Load reads configuration from environment variables.
// Local defaults make development easy, while production requires explicit URLs.
func Load() (Config, error) {
	cfg := Config{
		Environment: envOrDefault("APP_ENV", "development"),
		HTTPAddress: envOrDefault("HTTP_ADDRESS", ":8080"),
		DatabaseURL: envOrDefault("DATABASE_URL", localDatabaseURL),
		RedisURL:    envOrDefault("REDIS_URL", localRedisURL),
	}
	encryptionKey, err := base64.StdEncoding.DecodeString(envOrDefault("ENCRYPTION_KEY", localEncryptionKey))
	if err != nil || len(encryptionKey) != 32 {
		return Config{}, fmt.Errorf("invalid configuration: ENCRYPTION_KEY must be base64 for exactly 32 bytes")
	}
	cfg.EncryptionKey = encryptionKey

	if cfg.Environment == "production" {
		if os.Getenv("DATABASE_URL") == "" || os.Getenv("REDIS_URL") == "" || os.Getenv("ENCRYPTION_KEY") == "" {
			return Config{}, fmt.Errorf("DATABASE_URL, REDIS_URL, and ENCRYPTION_KEY are required in production")
		}
	}

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddress); err != nil {
		return fmt.Errorf("HTTP_ADDRESS must include a host and port, for example :8080")
	}
	if err := validateURL(c.DatabaseURL, "DATABASE_URL", "postgres", "postgresql"); err != nil {
		return err
	}
	return validateURL(c.RedisURL, "REDIS_URL", "redis", "rediss")
}

func validateURL(rawURL, name string, allowedSchemes ...string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s must be a valid URL", name)
	}
	for _, scheme := range allowedSchemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%s has unsupported scheme %q", name, parsed.Scheme)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
