package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"walletservice/internal/application"
	"walletservice/internal/domain"
	"walletservice/internal/infrastructure/postgres"
	"walletservice/internal/platform/clock"
	"walletservice/internal/platform/identity"
)

func TestConcurrentWithdrawalsPreserveBalanceInvariant(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, databaseURL, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := postgres.Migrate(ctx, store.Pool()); err != nil {
		t.Fatal(err)
	}

	service := application.NewService(store, identity.UUIDGenerator{}, clock.System{})
	scope := fmt.Sprintf("test-%d", time.Now().UnixNano())
	wallet, _, err := service.CreateWallet(ctx, application.CreateWalletCommand{
		Currency: "USD", Idempotency: application.Idempotency{Scope: scope, Key: "create"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupWalletTestData(store, scope, wallet.ID) })

	deposit, _ := domain.NewMoney(10_000, "USD")
	if _, err := service.Deposit(ctx, application.MoneyCommand{
		WalletID: wallet.ID, Money: deposit,
		Idempotency: application.Idempotency{Scope: scope, Key: "deposit"},
	}); err != nil {
		t.Fatal(err)
	}

	withdrawal, _ := domain.NewMoney(1_000, "USD")
	var succeeded atomic.Int64
	var insufficient atomic.Int64
	var waitGroup sync.WaitGroup
	for index := 0; index < 15; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			_, err := service.Withdraw(ctx, application.MoneyCommand{
				WalletID: wallet.ID, Money: withdrawal,
				Idempotency: application.Idempotency{Scope: scope, Key: fmt.Sprintf("withdraw-%d", index)},
			})
			switch {
			case err == nil:
				succeeded.Add(1)
			case errors.Is(err, domain.ErrInsufficientFunds):
				insufficient.Add(1)
			default:
				t.Errorf("withdrawal %d: %v", index, err)
			}
		}(index)
	}
	waitGroup.Wait()
	if succeeded.Load() != 10 || insufficient.Load() != 5 {
		t.Fatalf("expected 10 successes and 5 rejections, got %d and %d", succeeded.Load(), insufficient.Load())
	}
	updated, err := service.GetWallet(ctx, wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BalanceMinor != 0 {
		t.Fatalf("expected zero balance, got %d", updated.BalanceMinor)
	}
	page, err := service.ListHistory(ctx, wallet.ID, 0, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 11 {
		t.Fatalf("expected 11 committed history entries, got %d", len(page.Items))
	}
}

func TestConcurrentOpposingTransfersPreserveTotalMoney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, databaseURL, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := postgres.Migrate(ctx, store.Pool()); err != nil {
		t.Fatal(err)
	}

	service := application.NewService(store, identity.UUIDGenerator{}, clock.System{})
	scope := fmt.Sprintf("transfer-test-%d", time.Now().UnixNano())
	first, _, err := service.CreateWallet(ctx, application.CreateWalletCommand{
		Currency: "USD", Idempotency: application.Idempotency{Scope: scope, Key: "create-first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreateWallet(ctx, application.CreateWalletCommand{
		Currency: "USD", Idempotency: application.Idempotency{Scope: scope, Key: "create-second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupTwoWalletTestData(store, scope, first.ID, second.ID) })

	funding, _ := domain.NewMoney(10_000, "USD")
	for index, walletID := range []string{first.ID, second.ID} {
		if _, err := service.Deposit(ctx, application.MoneyCommand{
			WalletID: walletID, Money: funding,
			Idempotency: application.Idempotency{Scope: scope, Key: fmt.Sprintf("deposit-%d", index)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	amount, _ := domain.NewMoney(100, "USD")
	const transferPairs = 25
	errorsChannel := make(chan error, transferPairs*2)
	var waitGroup sync.WaitGroup
	for index := 0; index < transferPairs; index++ {
		for direction, wallets := range [][2]string{{first.ID, second.ID}, {second.ID, first.ID}} {
			waitGroup.Add(1)
			go func(index, direction int, source, destination string) {
				defer waitGroup.Done()
				_, err := service.Transfer(ctx, application.TransferCommand{
					SourceWalletID: source, DestinationWalletID: destination, Money: amount,
					Idempotency: application.Idempotency{Scope: scope, Key: fmt.Sprintf("transfer-%d-%d", index, direction)},
				})
				if err != nil {
					errorsChannel <- err
				}
			}(index, direction, wallets[0], wallets[1])
		}
	}
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("transfer failed: %v", err)
	}

	firstAfter, err := service.GetWallet(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfter, err := service.GetWallet(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total := firstAfter.BalanceMinor + secondAfter.BalanceMinor; total != 20_000 {
		t.Fatalf("expected total money to remain 20000, got %d", total)
	}
	if firstAfter.BalanceMinor != 10_000 || secondAfter.BalanceMinor != 10_000 {
		t.Fatalf("expected balanced opposing transfers, got %d and %d", firstAfter.BalanceMinor, secondAfter.BalanceMinor)
	}
}

func cleanupWalletTestData(store *postgres.Store, scope, walletID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = store.Pool().Exec(ctx, "DELETE FROM idempotency_records WHERE scope = $1", scope)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM ledger_entries WHERE wallet_id = $1", walletID)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM financial_transactions WHERE source_wallet_id = $1 OR destination_wallet_id = $1", walletID)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM wallets WHERE id = $1", walletID)
}

func cleanupTwoWalletTestData(store *postgres.Store, scope, firstWalletID, secondWalletID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = store.Pool().Exec(ctx, "DELETE FROM idempotency_records WHERE scope = $1", scope)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM ledger_entries WHERE wallet_id = $1 OR wallet_id = $2", firstWalletID, secondWalletID)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM financial_transactions WHERE source_wallet_id IN ($1, $2) OR destination_wallet_id IN ($1, $2)", firstWalletID, secondWalletID)
	_, _ = store.Pool().Exec(ctx, "DELETE FROM wallets WHERE id IN ($1, $2)", firstWalletID, secondWalletID)
}
