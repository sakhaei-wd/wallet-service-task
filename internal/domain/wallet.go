package domain

import "time"

type WalletStatus string

const (
	WalletActive WalletStatus = "ACTIVE"
	WalletFrozen WalletStatus = "FROZEN"
	WalletClosed WalletStatus = "CLOSED"
)

type Wallet struct {
	ID           string
	OwnerID      string
	Currency     string
	BalanceMinor int64
	Status       WalletStatus
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewWallet(id, ownerID, currency string, now time.Time) (Wallet, error) {
	if ownerID == "" {
		return Wallet{}, ErrInvalidOwner
	}
	currency, err := NormalizeCurrency(currency)
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{
		ID:        id,
		OwnerID:   ownerID,
		Currency:  currency,
		Status:    WalletActive,
		Version:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

func (w *Wallet) Credit(money Money, now time.Time) error {
	if err := w.validateMutation(money); err != nil {
		return err
	}
	balance, err := AddMinor(w.BalanceMinor, money.Minor)
	if err != nil {
		return err
	}
	w.BalanceMinor = balance
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

func (w *Wallet) Debit(money Money, now time.Time) error {
	if err := w.validateMutation(money); err != nil {
		return err
	}
	if w.BalanceMinor < money.Minor {
		return ErrInsufficientFunds
	}
	w.BalanceMinor -= money.Minor
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

func (w Wallet) validateMutation(money Money) error {
	if w.Status != WalletActive {
		return ErrWalletNotActive
	}
	if money.Minor <= 0 {
		return ErrInvalidAmount
	}
	if w.Currency != money.Currency {
		return ErrCurrencyMismatch
	}
	return nil
}
