package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"walletservice/internal/application"
	"walletservice/internal/domain"
)

type executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type repository struct {
	executor executor
}

func (r *repository) ReserveIdempotency(ctx context.Context, scope, key, requestHash, resourceType, resourceID string) (application.IdempotencyRecord, bool, error) {
	command, err := r.executor.Exec(ctx, `
		INSERT INTO idempotency_records (scope, key, request_hash, resource_type, resource_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (scope, key) DO NOTHING`, scope, key, requestHash, resourceType, resourceID)
	if err != nil {
		return application.IdempotencyRecord{}, false, fmt.Errorf("reserve idempotency key: %w", err)
	}
	if command.RowsAffected() == 1 {
		return application.IdempotencyRecord{RequestHash: requestHash, ResourceType: resourceType, ResourceID: resourceID}, true, nil
	}
	var record application.IdempotencyRecord
	err = r.executor.QueryRow(ctx, `
		SELECT request_hash, resource_type, resource_id::text
		FROM idempotency_records WHERE scope = $1 AND key = $2`, scope, key).
		Scan(&record.RequestHash, &record.ResourceType, &record.ResourceID)
	if err != nil {
		return application.IdempotencyRecord{}, false, fmt.Errorf("read idempotency key: %w", err)
	}
	return record, false, nil
}

func (r *repository) CreateWallet(ctx context.Context, wallet domain.Wallet) error {
	_, err := r.executor.Exec(ctx, `
		INSERT INTO wallets (id, owner_id, currency, balance_minor, status, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		wallet.ID, wallet.OwnerID, wallet.Currency, wallet.BalanceMinor, wallet.Status, wallet.Version, wallet.CreatedAt, wallet.UpdatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "wallets_owner_id_key" {
			return domain.ErrOwnerHasWallet
		}
		return fmt.Errorf("create wallet: %w", err)
	}
	return nil
}

func (r *repository) GetWallet(ctx context.Context, walletID string) (domain.Wallet, error) {
	return r.queryWallet(ctx, `
		SELECT id::text, owner_id::text, currency, balance_minor, status, version, created_at, updated_at
		FROM wallets WHERE id = $1`, walletID)
}

func (r *repository) GetWalletForUpdate(ctx context.Context, walletID string) (domain.Wallet, error) {
	return r.queryWallet(ctx, `
		SELECT id::text, owner_id::text, currency, balance_minor, status, version, created_at, updated_at
		FROM wallets WHERE id = $1 FOR UPDATE`, walletID)
}

func (r *repository) queryWallet(ctx context.Context, query, walletID string) (domain.Wallet, error) {
	var wallet domain.Wallet
	err := r.executor.QueryRow(ctx, query, walletID).Scan(
		&wallet.ID, &wallet.OwnerID, &wallet.Currency, &wallet.BalanceMinor, &wallet.Status,
		&wallet.Version, &wallet.CreatedAt, &wallet.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("get wallet: %w", err)
	}
	return wallet, nil
}

func (r *repository) UpdateWallet(ctx context.Context, wallet domain.Wallet) error {
	command, err := r.executor.Exec(ctx, `
		UPDATE wallets
		SET balance_minor = $2, status = $3, version = $4, updated_at = $5
		WHERE id = $1`, wallet.ID, wallet.BalanceMinor, wallet.Status, wallet.Version, wallet.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrWalletNotFound
	}
	return nil
}

func (r *repository) CreateFinancialTransaction(ctx context.Context, transaction domain.FinancialTransaction) error {
	_, err := r.executor.Exec(ctx, `
		INSERT INTO financial_transactions
			(id, type, amount_minor, currency, source_wallet_id, destination_wallet_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		transaction.ID, transaction.Type, transaction.AmountMinor, transaction.Currency,
		transaction.SourceWalletID, transaction.DestinationWalletID, transaction.CreatedAt)
	if err != nil {
		return fmt.Errorf("create financial transaction: %w", err)
	}
	return nil
}

func (r *repository) CreateLedgerEntry(ctx context.Context, entry domain.LedgerEntry) (domain.LedgerEntry, error) {
	err := r.executor.QueryRow(ctx, `
		INSERT INTO ledger_entries
			(transaction_id, wallet_id, direction, amount_minor, balance_before_minor, balance_after_minor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING sequence`, entry.TransactionID, entry.WalletID, entry.Direction, entry.AmountMinor,
		entry.BalanceBeforeMinor, entry.BalanceAfterMinor, entry.CreatedAt).Scan(&entry.Sequence)
	if err != nil {
		return domain.LedgerEntry{}, fmt.Errorf("create ledger entry: %w", err)
	}
	return entry, nil
}

func (r *repository) GetOperationResult(ctx context.Context, transactionID string) (domain.OperationResult, error) {
	var transaction domain.FinancialTransaction
	err := r.executor.QueryRow(ctx, `
		SELECT id::text, type, amount_minor, currency, source_wallet_id::text,
		       destination_wallet_id::text, created_at
		FROM financial_transactions WHERE id = $1`, transactionID).Scan(
		&transaction.ID, &transaction.Type, &transaction.AmountMinor, &transaction.Currency,
		&transaction.SourceWalletID, &transaction.DestinationWalletID, &transaction.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OperationResult{}, domain.ErrTransactionNotFound
	}
	if err != nil {
		return domain.OperationResult{}, fmt.Errorf("get financial transaction: %w", err)
	}

	rows, err := r.executor.Query(ctx, `
		SELECT sequence, transaction_id::text, wallet_id::text, direction, amount_minor,
		       balance_before_minor, balance_after_minor, created_at
		FROM ledger_entries WHERE transaction_id = $1 ORDER BY sequence`, transactionID)
	if err != nil {
		return domain.OperationResult{}, fmt.Errorf("query ledger entries: %w", err)
	}
	defer rows.Close()
	entries := make([]domain.LedgerEntry, 0, 2)
	for rows.Next() {
		var entry domain.LedgerEntry
		if err := rows.Scan(&entry.Sequence, &entry.TransactionID, &entry.WalletID, &entry.Direction,
			&entry.AmountMinor, &entry.BalanceBeforeMinor, &entry.BalanceAfterMinor, &entry.CreatedAt); err != nil {
			return domain.OperationResult{}, fmt.Errorf("scan ledger entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return domain.OperationResult{}, fmt.Errorf("iterate ledger entries: %w", err)
	}
	return domain.OperationResult{Transaction: transaction, Entries: entries}, nil
}

func (r *repository) ListHistory(ctx context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
	typeFilter := ""
	if transactionType != nil {
		typeFilter = string(*transactionType)
	}
	rows, err := r.executor.Query(ctx, `
		SELECT le.sequence, ft.id::text, ft.type, le.direction, le.amount_minor, ft.currency,
		       le.balance_after_minor,
		       CASE WHEN le.direction = 'DEBIT' THEN ft.destination_wallet_id::text ELSE ft.source_wallet_id::text END,
		       le.created_at
		FROM ledger_entries le
		JOIN financial_transactions ft ON ft.id = le.transaction_id
		WHERE le.wallet_id = $1
		  AND ($2::bigint = 0 OR le.sequence < $2)
		  AND ($3::text = '' OR ft.type = $3)
		ORDER BY le.sequence DESC
		LIMIT $4`, walletID, cursor, typeFilter, limit+1)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("query transaction history: %w", err)
	}
	defer rows.Close()
	items := make([]domain.HistoryItem, 0, limit+1)
	for rows.Next() {
		var item domain.HistoryItem
		if err := rows.Scan(&item.Sequence, &item.TransactionID, &item.Type, &item.Direction,
			&item.AmountMinor, &item.Currency, &item.BalanceAfterMinor,
			&item.CounterpartyWalletID, &item.CreatedAt); err != nil {
			return domain.HistoryPage{}, fmt.Errorf("scan transaction history: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.HistoryPage{}, fmt.Errorf("iterate transaction history: %w", err)
	}
	page := domain.HistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].Sequence
	}
	return page, nil
}

var _ application.TransactionStore = (*repository)(nil)
