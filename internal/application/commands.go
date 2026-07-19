package application

import "walletservice/internal/domain"

type Idempotency struct {
	Key string
}

type CreateWalletCommand struct {
	OwnerID     string
	Currency    string
	Idempotency Idempotency
}

type MoneyCommand struct {
	OwnerID     string
	WalletID    string
	Money       domain.Money
	Idempotency Idempotency
}

type TransferCommand struct {
	OwnerID             string
	SourceWalletID      string
	DestinationWalletID string
	Money               domain.Money
	Idempotency         Idempotency
}
