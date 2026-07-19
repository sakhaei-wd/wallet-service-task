package domain

import "time"

type TransactionType string

const (
	TransactionDeposit    TransactionType = "DEPOSIT"
	TransactionWithdrawal TransactionType = "WITHDRAWAL"
	TransactionTransfer   TransactionType = "TRANSFER"
)

type EntryDirection string

const (
	EntryDebit  EntryDirection = "DEBIT"
	EntryCredit EntryDirection = "CREDIT"
)

type FinancialTransaction struct {
	ID                  string
	Type                TransactionType
	AmountMinor         int64
	Currency            string
	SourceWalletID      *string
	DestinationWalletID *string
	CreatedAt           time.Time
}

type LedgerEntry struct {
	Sequence           int64
	TransactionID      string
	WalletID           string
	Direction          EntryDirection
	AmountMinor        int64
	BalanceBeforeMinor int64
	BalanceAfterMinor  int64
	CreatedAt          time.Time
}

type OperationResult struct {
	Transaction FinancialTransaction
	Entries     []LedgerEntry
	Replayed    bool
}

type HistoryItem struct {
	Sequence             int64
	TransactionID        string
	Type                 TransactionType
	Direction            EntryDirection
	AmountMinor          int64
	Currency             string
	BalanceAfterMinor    int64
	CounterpartyWalletID *string
	CreatedAt            time.Time
}

type HistoryPage struct {
	Items      []HistoryItem
	NextCursor int64
}
