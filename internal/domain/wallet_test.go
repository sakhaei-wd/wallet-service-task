package domain_test

import (
	"errors"
	"math"
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

func TestWalletMutationValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		wallet    domain.Wallet
		money     domain.Money
		credit    bool
		wantErr   error
		wantValue int64
	}{
		{name: "credit success", wallet: domain.Wallet{Currency: "USD", BalanceMinor: 100, Status: domain.WalletActive, Version: 1}, money: domain.Money{Minor: 25, Currency: "USD"}, credit: true, wantValue: 125},
		{name: "debit success", wallet: domain.Wallet{Currency: "USD", BalanceMinor: 100, Status: domain.WalletActive, Version: 1}, money: domain.Money{Minor: 25, Currency: "USD"}, wantValue: 75},
		{name: "closed wallet", wallet: domain.Wallet{Currency: "USD", BalanceMinor: 100, Status: domain.WalletClosed}, money: domain.Money{Minor: 25, Currency: "USD"}, credit: true, wantErr: domain.ErrWalletNotActive, wantValue: 100},
		{name: "currency mismatch", wallet: domain.Wallet{Currency: "USD", BalanceMinor: 100, Status: domain.WalletActive}, money: domain.Money{Minor: 25, Currency: "EUR"}, credit: true, wantErr: domain.ErrCurrencyMismatch, wantValue: 100},
		{name: "zero amount", wallet: domain.Wallet{Currency: "USD", BalanceMinor: 100, Status: domain.WalletActive}, money: domain.Money{Currency: "USD"}, credit: true, wantErr: domain.ErrInvalidAmount, wantValue: 100},
		{name: "credit overflow", wallet: domain.Wallet{Currency: "USD", BalanceMinor: math.MaxInt64, Status: domain.WalletActive}, money: domain.Money{Minor: 1, Currency: "USD"}, credit: true, wantErr: domain.ErrAmountOverflow, wantValue: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wallet := test.wallet
			var err error
			if test.credit {
				err = wallet.Credit(test.money, now)
			} else {
				err = wallet.Debit(test.money, now)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			if wallet.BalanceMinor != test.wantValue {
				t.Fatalf("expected balance %d, got %d", test.wantValue, wallet.BalanceMinor)
			}
			if test.wantErr == nil && (wallet.Version != test.wallet.Version+1 || !wallet.UpdatedAt.Equal(now)) {
				t.Fatalf("successful mutation did not update metadata: %+v", wallet)
			}
		})
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
