package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment       string
	HTTPAddress       string
	DatabaseURL       string
	DatabaseMaxConns  int32
	RunMigrations     bool
	SwaggerEnabled    bool
	ShutdownTimeout   time.Duration
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

func Load() (Config, error) {
	environmentName := environment("APP_ENV", "development")
	maxConnections, err := parseEnvironmentInt("DATABASE_MAX_CONNECTIONS", 20)
	if err != nil {
		return Config{}, err
	}
	runMigrations, err := parseEnvironmentBool("RUN_MIGRATIONS", false)
	if err != nil {
		return Config{}, err
	}
	swaggerEnabled, err := parseEnvironmentBool("SWAGGER_ENABLED", environmentName != "production")
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := parseEnvironmentDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := parseEnvironmentDuration("REQUEST_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	readHeaderTimeout, err := parseEnvironmentDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := parseEnvironmentDuration("HTTP_READ_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := parseEnvironmentDuration("HTTP_WRITE_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := parseEnvironmentDuration("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	configuration := Config{
		Environment:       environmentName,
		HTTPAddress:       environment("HTTP_ADDRESS", ":8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		DatabaseMaxConns:  int32(maxConnections),
		RunMigrations:     runMigrations,
		SwaggerEnabled:    swaggerEnabled,
		ShutdownTimeout:   shutdownTimeout,
		RequestTimeout:    requestTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	if configuration.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if configuration.DatabaseMaxConns < 2 {
		return Config{}, errors.New("DATABASE_MAX_CONNECTIONS must be at least 2")
	}
	return configuration, nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseEnvironmentInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func parseEnvironmentBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func parseEnvironmentDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return parsed, nil
}

func (c Config) ValidateProduction() error {
	if c.Environment == "production" && c.RunMigrations {
		return fmt.Errorf("RUN_MIGRATIONS should be disabled in production and run as a separate release step")
	}
	return nil
}
