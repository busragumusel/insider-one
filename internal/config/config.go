package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL        string
	ProviderURL        string
	SMSProviderURL     string
	EmailProviderURL   string
	PushProviderURL    string
	ListenAddress      string
	LogLevel           string
	OtelEnabled        bool
	OtelEndpoint       string
	ServiceName        string
	WorkerConcurrency  int
	WorkerClaimLimit   int
	APIKeys            []string
	RateLimitPerSecond int
}

func Load() Config {
	databaseURL := getenv("DATABASE_URL", "postgres://insider:insider@localhost:5432/insider_one?sslmode=disable")
	return Config{
		DatabaseURL:        databaseURL,
		ProviderURL:        getenv("PROVIDER_URL", "noop://accepted"),
		SMSProviderURL:     getenv("SMS_PROVIDER_URL", ""),
		EmailProviderURL:   getenv("EMAIL_PROVIDER_URL", ""),
		PushProviderURL:    getenv("PUSH_PROVIDER_URL", ""),
		ListenAddress:      getenv("LISTEN_ADDR", ":8080"),
		LogLevel:           getenv("LOG_LEVEL", "info"),
		OtelEnabled:        getenvBool("OTEL_ENABLED", true),
		OtelEndpoint:       getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317"),
		ServiceName:        getenv("OTEL_SERVICE_NAME", "insider-one-notifications"),
		WorkerConcurrency:  getenvInt("WORKER_CONCURRENCY", 4),
		WorkerClaimLimit:   getenvInt("WORKER_CLAIM_LIMIT", 25),
		APIKeys:            getenvCSV("API_KEYS"),
		RateLimitPerSecond: getenvInt("RATE_LIMIT_PER_SECOND", 100),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenvCSV(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}
