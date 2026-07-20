package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"walletservice/internal/application"
	"walletservice/internal/config"
	"walletservice/internal/infrastructure/postgres"
	"walletservice/internal/interfaces/httpapi"
	"walletservice/internal/platform/clock"
	"walletservice/internal/platform/identity"
)

func main() {
	logger := newLogger()
	if err := run(logger); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	if err := configuration.ValidateProduction(); err != nil {
		return err
	}

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(rootContext, configuration.DatabaseURL, configuration.DatabaseMaxConns)
	if err != nil {
		return err
	}
	defer store.Close()
	if configuration.RunMigrations {
		if err := postgres.Migrate(rootContext, store.Pool()); err != nil {
			return err
		}
	}

	idGenerator := identity.UUIDGenerator{}
	service := application.NewService(store, idGenerator, clock.System{})
	handler := httpapi.NewHandler(service, logger)
	server := &http.Server{
		Addr:              configuration.HTTPAddress,
		Handler:           handler.Routes(configuration.RequestTimeout, idGenerator, configuration.SwaggerEnabled),
		ReadHeaderTimeout: configuration.ReadHeaderTimeout,
		ReadTimeout:       configuration.ReadTimeout,
		WriteTimeout:      configuration.WriteTimeout,
		IdleTimeout:       configuration.IdleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("wallet service started", "address", server.Addr, "environment", configuration.Environment)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-rootContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), configuration.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		logger.Info("wallet service stopped gracefully")
	}
	return nil
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
