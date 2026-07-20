package config

import (
	"strings"
	"testing"
)

func TestLoadValidConfiguration(t *testing.T) {
	setValidEnvironment(t)
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DatabaseMaxConns != 20 || !configuration.RunMigrations || !configuration.SwaggerEnabled || configuration.HTTPAddress != ":9090" {
		t.Fatalf("unexpected configuration: %+v", configuration)
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		variable string
		value    string
		message  string
	}{
		{name: "missing database URL", variable: "DATABASE_URL", value: "", message: "DATABASE_URL is required"},
		{name: "invalid connection count", variable: "DATABASE_MAX_CONNECTIONS", value: "many", message: "must be an integer"},
		{name: "too few connections", variable: "DATABASE_MAX_CONNECTIONS", value: "1", message: "must be at least 2"},
		{name: "invalid boolean", variable: "RUN_MIGRATIONS", value: "sometimes", message: "must be a boolean"},
		{name: "invalid Swagger boolean", variable: "SWAGGER_ENABLED", value: "sometimes", message: "must be a boolean"},
		{name: "invalid duration", variable: "REQUEST_TIMEOUT", value: "soon", message: "must be a duration"},
		{name: "nonpositive duration", variable: "HTTP_IDLE_TIMEOUT", value: "0s", message: "must be greater than zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv(test.variable, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}

func TestSwaggerDefaultsOffInProduction(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("RUN_MIGRATIONS", "false")
	t.Setenv("SWAGGER_ENABLED", "")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.SwaggerEnabled {
		t.Fatal("expected Swagger UI to default off in production")
	}
}

func TestProductionRejectsAutomaticMigrations(t *testing.T) {
	configuration := Config{Environment: "production", RunMigrations: true}
	if err := configuration.ValidateProduction(); err == nil {
		t.Fatal("expected production migration validation error")
	}
	configuration.RunMigrations = false
	if err := configuration.ValidateProduction(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("DATABASE_MAX_CONNECTIONS", "20")
	t.Setenv("RUN_MIGRATIONS", "true")
	t.Setenv("SWAGGER_ENABLED", "true")
	t.Setenv("APP_ENV", "development")
	t.Setenv("HTTP_ADDRESS", ":9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("REQUEST_TIMEOUT", "10s")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "5s")
	t.Setenv("HTTP_READ_TIMEOUT", "15s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "15s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "60s")
}
