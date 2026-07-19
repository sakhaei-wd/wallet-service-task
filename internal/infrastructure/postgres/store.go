package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"walletservice/internal/application"
	"walletservice/internal/domain"
)

const maxTransactionAttempts = 3

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*Store, error) {
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	configuration.MaxConns = maxConnections
	configuration.MinConns = 1
	configuration.MaxConnLifetime = time.Hour
	configuration.MaxConnIdleTime = 15 * time.Minute
	configuration.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) WithinTransaction(ctx context.Context, operation func(context.Context, application.TransactionStore) error) error {
	for attempt := 1; attempt <= maxTransactionAttempts; attempt++ {
		err := s.runTransaction(ctx, operation)
		if err == nil || !isRetryable(err) || attempt == maxTransactionAttempts {
			return err
		}
		backoff := time.Duration(attempt*attempt) * 10 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (s *Store) runTransaction(ctx context.Context, operation func(context.Context, application.TransactionStore) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return fmt.Errorf("begin database transaction: %w", err)
	}
	repository := &repository{executor: tx}
	if err := operation(ctx, repository); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback database transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit database transaction: %w", err)
	}
	return nil
}

func (s *Store) GetWallet(ctx context.Context, walletID string) (domain.Wallet, error) {
	return (&repository{executor: s.pool}).GetWallet(ctx, walletID)
}

func (s *Store) GetOperationResult(ctx context.Context, transactionID string) (domain.OperationResult, error) {
	return (&repository{executor: s.pool}).GetOperationResult(ctx, transactionID)
}

func (s *Store) ListHistory(ctx context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
	return (&repository{executor: s.pool}).ListHistory(ctx, walletID, cursor, limit, transactionType)
}

func isRetryable(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40P01" || postgresError.Code == "40001"
}

var _ application.Store = (*Store)(nil)
