package domain

import "errors"

var (
	ErrInvalidAmount       = errors.New("amount must be greater than zero")
	ErrInvalidCurrency     = errors.New("currency is not supported")
	ErrCurrencyMismatch    = errors.New("currencies do not match")
	ErrInsufficientFunds   = errors.New("wallet has insufficient funds")
	ErrSameWallet          = errors.New("source and destination wallets must differ")
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrWalletNotActive     = errors.New("wallet is not active")
	ErrAmountOverflow      = errors.New("amount exceeds the supported range")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrIdempotencyConflict = errors.New("idempotency key was used with a different request")
	ErrInvalidOwner        = errors.New("owner ID is required")
	ErrWalletAccessDenied  = errors.New("wallet access denied")
	ErrOwnerHasWallet      = errors.New("owner already has a wallet")
)
