package domain_test

import (
	"errors"
	"testing"
	"time"

	"walletservice/internal/domain"
)

func TestWalletDebitNeverMakesBalanceNegative(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wallet, err := domain.NewWallet("wallet", "USD", now)
	if err != nil {
		t.Fatal(err)
	}
	money, _ := domain.NewMoney(100, "USD")
	if err := wallet.Credit(money, now); err != nil {
		t.Fatal(err)
	}
	tooMuch, _ := domain.NewMoney(101, "USD")
	if err := wallet.Debit(tooMuch, now); !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("expected insufficient funds, got %v", err)
	}
	if wallet.BalanceMinor != 100 {
		t.Fatalf("failed debit changed balance to %d", wallet.BalanceMinor)
	}
}

func TestFrozenWalletRejectsMutations(t *testing.T) {
	t.Parallel()
	wallet := domain.Wallet{Currency: "USD", Status: domain.WalletFrozen}
	money, _ := domain.NewMoney(100, "USD")
	if err := wallet.Credit(money, time.Now()); !errors.Is(err, domain.ErrWalletNotActive) {
		t.Fatalf("expected inactive wallet error, got %v", err)
	}
}
