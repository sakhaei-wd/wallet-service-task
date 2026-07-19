package application

import (
	"context"

	"walletservice/internal/domain"
)

type IdempotencyRecord struct {
	RequestHash  string
	ResourceType string
	ResourceID   string
}

type TransactionStore interface {
	ReserveIdempotency(ctx context.Context, scope, key, requestHash, resourceType, resourceID string) (IdempotencyRecord, bool, error)
	CreateWallet(ctx context.Context, wallet domain.Wallet) error
	GetWallet(ctx context.Context, walletID string) (domain.Wallet, error)
	GetWalletForUpdate(ctx context.Context, walletID string) (domain.Wallet, error)
	UpdateWallet(ctx context.Context, wallet domain.Wallet) error
	CreateFinancialTransaction(ctx context.Context, transaction domain.FinancialTransaction) error
	CreateLedgerEntry(ctx context.Context, entry domain.LedgerEntry) (domain.LedgerEntry, error)
	GetOperationResult(ctx context.Context, transactionID string) (domain.OperationResult, error)
}

type Store interface {
	WithinTransaction(ctx context.Context, operation func(context.Context, TransactionStore) error) error
	GetWallet(ctx context.Context, walletID string) (domain.Wallet, error)
	GetOperationResult(ctx context.Context, transactionID string) (domain.OperationResult, error)
	ListHistory(ctx context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error)
	Ping(ctx context.Context) error
}
