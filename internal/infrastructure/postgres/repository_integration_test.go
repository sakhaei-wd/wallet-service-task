package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"walletservice/internal/application"
	"walletservice/internal/domain"
	"walletservice/internal/platform/clock"
	"walletservice/internal/platform/identity"
)

func TestMigrationsAreIdempotent(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Migrate(ctx, store.Pool()); err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	var count int
	if err := store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = $1", "001_initial.sql").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected migration to be recorded once, got %d", count)
	}
}

func TestRepositoryWalletLifecycle(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := integrationUUID(t)
	cleanupIntegrationData(t, store, "", id)
	repository := &repository{executor: store.Pool()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	wallet, err := domain.NewWallet(id, "USD", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWallet(ctx, wallet); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.GetWallet(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != wallet.ID || loaded.Currency != wallet.Currency || loaded.BalanceMinor != wallet.BalanceMinor ||
		loaded.Status != wallet.Status || loaded.Version != wallet.Version ||
		!loaded.CreatedAt.Equal(wallet.CreatedAt) || !loaded.UpdatedAt.Equal(wallet.UpdatedAt) {
		t.Fatalf("wallet round trip mismatch:\nwant %+v\n got %+v", wallet, loaded)
	}
	money, _ := domain.NewMoney(250, "USD")
	if err := loaded.Credit(money, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateWallet(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	updated, err := repository.GetWallet(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BalanceMinor != 250 || updated.Version != 2 {
		t.Fatalf("wallet update was not persisted: %+v", updated)
	}
	_, err = repository.GetWallet(ctx, integrationUUID(t))
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected wallet not found, got %v", err)
	}
}

func TestDatabaseRejectsInvalidWalletState(t *testing.T) {
	store := openIntegrationStore(t)
	repository := &repository{executor: store.Pool()}
	tests := []struct {
		name   string
		mutate func(*domain.Wallet)
	}{
		{name: "negative balance", mutate: func(wallet *domain.Wallet) { wallet.BalanceMinor = -1 }},
		{name: "invalid status", mutate: func(wallet *domain.Wallet) { wallet.Status = "UNKNOWN" }},
		{name: "zero version", mutate: func(wallet *domain.Wallet) { wallet.Version = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			wallet, _ := domain.NewWallet(integrationUUID(t), "USD", time.Now().UTC())
			test.mutate(&wallet)
			err := repository.CreateWallet(ctx, wallet)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
				t.Fatalf("expected check violation, got %v", err)
			}
		})
	}
}

func TestWithinTransactionRollsBackAllFinancialWrites(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	walletID, transactionID := integrationUUID(t), integrationUUID(t)
	cleanupIntegrationData(t, store, "rollback-test", walletID)
	repository := &repository{executor: store.Pool()}
	now := time.Now().UTC()
	wallet, _ := domain.NewWallet(walletID, "USD", now)
	wallet.BalanceMinor = 100
	if err := repository.CreateWallet(ctx, wallet); err != nil {
		t.Fatal(err)
	}

	errExpected := errors.New("abort after all writes")
	err := store.WithinTransaction(ctx, func(ctx context.Context, tx application.TransactionStore) error {
		locked, err := tx.GetWalletForUpdate(ctx, walletID)
		if err != nil {
			return err
		}
		money, _ := domain.NewMoney(25, "USD")
		before := locked.BalanceMinor
		if err := locked.Credit(money, now); err != nil {
			return err
		}
		destination := walletID
		transaction := domain.FinancialTransaction{
			ID: transactionID, Type: domain.TransactionDeposit, AmountMinor: 25,
			Currency: "USD", DestinationWalletID: &destination, CreatedAt: now,
		}
		if err := tx.CreateFinancialTransaction(ctx, transaction); err != nil {
			return err
		}
		if err := tx.UpdateWallet(ctx, locked); err != nil {
			return err
		}
		if _, err := tx.CreateLedgerEntry(ctx, domain.LedgerEntry{
			TransactionID: transactionID, WalletID: walletID, Direction: domain.EntryCredit,
			AmountMinor: 25, BalanceBeforeMinor: before, BalanceAfterMinor: locked.BalanceMinor, CreatedAt: now,
		}); err != nil {
			return err
		}
		return errExpected
	})
	if !errors.Is(err, errExpected) {
		t.Fatalf("expected rollback error, got %v", err)
	}
	unchanged, err := store.GetWallet(ctx, walletID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.BalanceMinor != 100 {
		t.Fatalf("rollback left balance at %d", unchanged.BalanceMinor)
	}
	if _, err := store.GetOperationResult(ctx, transactionID); !errors.Is(err, domain.ErrTransactionNotFound) {
		t.Fatalf("rolled-back transaction remained visible: %v", err)
	}
	page, err := store.ListHistory(ctx, walletID, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rollback left %d history entries", len(page.Items))
	}
}

func TestConstraintFailureRollsBackEarlierWrites(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstID, invalidID := integrationUUID(t), integrationUUID(t)
	cleanupIntegrationData(t, store, "", firstID, invalidID)
	now := time.Now().UTC()
	err := store.WithinTransaction(ctx, func(ctx context.Context, tx application.TransactionStore) error {
		first, _ := domain.NewWallet(firstID, "USD", now)
		if err := tx.CreateWallet(ctx, first); err != nil {
			return err
		}
		invalid, _ := domain.NewWallet(invalidID, "USD", now)
		invalid.BalanceMinor = -1
		return tx.CreateWallet(ctx, invalid)
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("expected check violation, got %v", err)
	}
	if _, err := store.GetWallet(ctx, firstID); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("earlier insert was not rolled back: %v", err)
	}
}

func TestDatabaseRejectsInvalidLedgerArithmetic(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	walletID, transactionID := integrationUUID(t), integrationUUID(t)
	cleanupIntegrationData(t, store, "", walletID)
	repository := &repository{executor: store.Pool()}
	now := time.Now().UTC()
	wallet, _ := domain.NewWallet(walletID, "USD", now)
	if err := repository.CreateWallet(ctx, wallet); err != nil {
		t.Fatal(err)
	}
	destination := walletID
	if err := repository.CreateFinancialTransaction(ctx, domain.FinancialTransaction{
		ID: transactionID, Type: domain.TransactionDeposit, AmountMinor: 100,
		Currency: "USD", DestinationWalletID: &destination, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := repository.CreateLedgerEntry(ctx, domain.LedgerEntry{
		TransactionID: transactionID, WalletID: walletID, Direction: domain.EntryCredit,
		AmountMinor: 100, BalanceBeforeMinor: 0, BalanceAfterMinor: 99, CreatedAt: now,
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("expected ledger check violation, got %v", err)
	}
	var count int
	if err := store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM ledger_entries WHERE transaction_id = $1", transactionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid ledger entry was persisted")
	}
}

func TestHistoryRepositoryPaginationAndFiltering(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	service := application.NewService(store, identity.UUIDGenerator{}, clock.System{})
	scope := fmt.Sprintf("history-%d", time.Now().UnixNano())
	wallet, _, err := service.CreateWallet(ctx, application.CreateWalletCommand{Currency: "USD", Idempotency: application.Idempotency{Scope: scope, Key: "create"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupIntegrationData(t, store, scope, wallet.ID)
	for index, amount := range []int64{100, 200, 300} {
		money, _ := domain.NewMoney(amount, "USD")
		if _, err := service.Deposit(ctx, application.MoneyCommand{WalletID: wallet.ID, Money: money, Idempotency: application.Idempotency{Scope: scope, Key: fmt.Sprintf("deposit-%d", index)}}); err != nil {
			t.Fatal(err)
		}
	}
	withdrawal, _ := domain.NewMoney(50, "USD")
	if _, err := service.Withdraw(ctx, application.MoneyCommand{WalletID: wallet.ID, Money: withdrawal, Idempotency: application.Idempotency{Scope: scope, Key: "withdraw"}}); err != nil {
		t.Fatal(err)
	}

	firstPage, err := store.ListHistory(ctx, wallet.ID, 0, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondPage, err := store.ListHistory(ctx, wallet.ID, firstPage.NextCursor, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 2 || len(secondPage.Items) != 2 || firstPage.NextCursor == 0 || secondPage.NextCursor != 0 {
		t.Fatalf("unexpected pages: first=%+v second=%+v", firstPage, secondPage)
	}
	seen := make(map[string]struct{}, 4)
	for _, item := range append(firstPage.Items, secondPage.Items...) {
		seen[item.TransactionID] = struct{}{}
	}
	if len(seen) != 4 {
		t.Fatalf("pagination duplicated or omitted transactions: %v", seen)
	}
	filter := domain.TransactionDeposit
	deposits, err := store.ListHistory(ctx, wallet.ID, 0, 100, &filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(deposits.Items) != 3 {
		t.Fatalf("expected 3 deposits, got %d", len(deposits.Items))
	}
}

func TestConcurrentIdempotentDepositIsAppliedOnce(t *testing.T) {
	store := openIntegrationStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	service := application.NewService(store, identity.UUIDGenerator{}, clock.System{})
	scope := fmt.Sprintf("idempotency-%d", time.Now().UnixNano())
	wallet, _, err := service.CreateWallet(ctx, application.CreateWalletCommand{Currency: "USD", Idempotency: application.Idempotency{Scope: scope, Key: "create"}})
	if err != nil {
		t.Fatal(err)
	}
	cleanupIntegrationData(t, store, scope, wallet.ID)
	money, _ := domain.NewMoney(100, "USD")
	command := application.MoneyCommand{WalletID: wallet.ID, Money: money, Idempotency: application.Idempotency{Scope: scope, Key: "same-deposit"}}

	const requestCount = 20
	results := make(chan domain.OperationResult, requestCount)
	errorsChannel := make(chan error, requestCount)
	var waitGroup sync.WaitGroup
	for range requestCount {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := service.Deposit(ctx, command)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- result
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("idempotent request failed: %v", err)
	}
	transactionIDs := make(map[string]struct{})
	for result := range results {
		transactionIDs[result.Transaction.ID] = struct{}{}
	}
	if len(transactionIDs) != 1 {
		t.Fatalf("expected one transaction ID, got %v", transactionIDs)
	}
	updated, err := service.GetWallet(ctx, wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BalanceMinor != 100 {
		t.Fatalf("deposit was applied more than once: balance=%d", updated.BalanceMinor)
	}
	history, err := service.ListHistory(ctx, wallet.ID, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 {
		t.Fatalf("expected one ledger entry, got %d", len(history.Items))
	}
}

func openIntegrationStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := Open(ctx, databaseURL, 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := Migrate(ctx, store.Pool()); err != nil {
		t.Fatal(err)
	}
	return store
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value, err := (identity.UUIDGenerator{}).New()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func cleanupIntegrationData(t *testing.T, store *Store, scope string, walletIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if scope != "" {
			_, _ = store.Pool().Exec(ctx, "DELETE FROM idempotency_records WHERE scope = $1", scope)
		}
		_, _ = store.Pool().Exec(ctx, "DELETE FROM ledger_entries WHERE wallet_id = ANY($1::uuid[])", walletIDs)
		_, _ = store.Pool().Exec(ctx, "DELETE FROM financial_transactions WHERE source_wallet_id = ANY($1::uuid[]) OR destination_wallet_id = ANY($1::uuid[])", walletIDs)
		_, _ = store.Pool().Exec(ctx, "DELETE FROM wallets WHERE id = ANY($1::uuid[])", walletIDs)
	})
}
