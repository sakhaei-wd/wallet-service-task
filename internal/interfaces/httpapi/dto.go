package httpapi

import (
	"time"

	"walletservice/internal/domain"
)

type createWalletRequest struct {
	Currency string `json:"currency"`
}

type moneyRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type transferRequest struct {
	SourceWalletID      string `json:"sourceWalletId"`
	DestinationWalletID string `json:"destinationWalletId"`
	Amount              string `json:"amount"`
	Currency            string `json:"currency"`
}

type walletResponse struct {
	ID        string    `json:"id"`
	Currency  string    `json:"currency"`
	Balance   string    `json:"balance"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ledgerEntryResponse struct {
	WalletID     string `json:"walletId"`
	Direction    string `json:"direction"`
	BalanceAfter string `json:"balanceAfter"`
}

type operationResponse struct {
	ID                  string                `json:"id"`
	Type                string                `json:"type"`
	Amount              string                `json:"amount"`
	Currency            string                `json:"currency"`
	SourceWalletID      *string               `json:"sourceWalletId,omitempty"`
	DestinationWalletID *string               `json:"destinationWalletId,omitempty"`
	Entries             []ledgerEntryResponse `json:"entries"`
	CreatedAt           time.Time             `json:"createdAt"`
}

type historyItemResponse struct {
	ID                   string    `json:"id"`
	Type                 string    `json:"type"`
	Direction            string    `json:"direction"`
	Amount               string    `json:"amount"`
	Currency             string    `json:"currency"`
	BalanceAfter         string    `json:"balanceAfter"`
	CounterpartyWalletID *string   `json:"counterpartyWalletId,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
}

type historyResponse struct {
	Items      []historyItemResponse `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

func presentWallet(wallet domain.Wallet) walletResponse {
	return walletResponse{
		ID: wallet.ID, Currency: wallet.Currency,
		Balance: domain.FormatMinor(wallet.BalanceMinor, wallet.Currency),
		Status:  string(wallet.Status), Version: wallet.Version,
		CreatedAt: wallet.CreatedAt, UpdatedAt: wallet.UpdatedAt,
	}
}

func presentOperation(result domain.OperationResult) operationResponse {
	entries := make([]ledgerEntryResponse, 0, len(result.Entries))
	for _, entry := range result.Entries {
		entries = append(entries, ledgerEntryResponse{
			WalletID: entry.WalletID, Direction: string(entry.Direction),
			BalanceAfter: domain.FormatMinor(entry.BalanceAfterMinor, result.Transaction.Currency),
		})
	}
	return operationResponse{
		ID: result.Transaction.ID, Type: string(result.Transaction.Type),
		Amount:   domain.FormatMinor(result.Transaction.AmountMinor, result.Transaction.Currency),
		Currency: result.Transaction.Currency, SourceWalletID: result.Transaction.SourceWalletID,
		DestinationWalletID: result.Transaction.DestinationWalletID,
		Entries:             entries, CreatedAt: result.Transaction.CreatedAt,
	}
}
