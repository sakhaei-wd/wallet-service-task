package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"walletservice/internal/config"
	"walletservice/internal/infrastructure/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configuration, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := postgres.Open(ctx, configuration.DatabaseURL, configuration.DatabaseMaxConns)
	if err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := postgres.Migrate(ctx, store.Pool()); err != nil {
		logger.Error("apply migrations", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations applied")
}
