package application

import "walletservice/internal/domain"

type Idempotency struct {
	Scope string
	Key   string
}

type CreateWalletCommand struct {
	Currency    string
	Idempotency Idempotency
}

type MoneyCommand struct {
	WalletID    string
	Money       domain.Money
	Idempotency Idempotency
}

type TransferCommand struct {
	SourceWalletID      string
	DestinationWalletID string
	Money               domain.Money
	Idempotency         Idempotency
}
