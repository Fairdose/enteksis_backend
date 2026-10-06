package config

import (
	"os"
	"strings"
)

type Config struct {
	AppEnv         string
	HTTPAddr       string
	DatabaseURL    string
	AllowedOrigins []string
	AdminUsername  string
	AdminPassword  string
}

func Load() Config {
	return Config{
		AppEnv:         valueOrDefault("APP_ENV", "development"),
		HTTPAddr:       valueOrDefault("HTTP_ADDR", ":8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AllowedOrigins: splitAndTrim(valueOrDefault("ALLOWED_ORIGINS", "http://localhost:5173")),
		AdminUsername:  valueOrDefault("ADMIN_USERNAME", "admin"),
		AdminPassword:  valueOrDefault("ADMIN_PASSWORD", "enteksis-local-admin"),
	}
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func splitAndTrim(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
