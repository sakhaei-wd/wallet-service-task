package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"walletservice/internal/domain"
	"walletservice/internal/platform/clock"
	"walletservice/internal/platform/identity"
)

const (
	resourceWallet      = "WALLET"
	resourceTransaction = "TRANSACTION"
)

type Service struct {
	store Store
	ids   identity.Generator
	clock clock.Clock
}

func NewService(store Store, ids identity.Generator, applicationClock clock.Clock) *Service {
	return &Service{store: store, ids: ids, clock: applicationClock}
}

func (s *Service) CreateWallet(ctx context.Context, command CreateWalletCommand) (domain.Wallet, bool, error) {
	command.OwnerID = normalizeID(command.OwnerID)
	if command.OwnerID == "" {
		return domain.Wallet{}, false, domain.ErrInvalidOwner
	}
	currency, err := domain.NormalizeCurrency(command.Currency)
	if err != nil {
		return domain.Wallet{}, false, err
	}
	if err := validateIdempotency(command.Idempotency); err != nil {
		return domain.Wallet{}, false, err
	}
	walletID, err := s.ids.New()
	if err != nil {
		return domain.Wallet{}, false, fmt.Errorf("generate wallet ID: %w", err)
	}
	now := s.clock.Now()
	wallet, err := domain.NewWallet(walletID, command.OwnerID, currency, now)
	if err != nil {
		return domain.Wallet{}, false, err
	}
	hash := requestHash("create-wallet", command.OwnerID, currency)
	replayed := false

	err = s.store.WithinTransaction(ctx, func(ctx context.Context, tx TransactionStore) error {
		record, reserved, err := tx.ReserveIdempotency(ctx, command.OwnerID, command.Idempotency.Key, hash, resourceWallet, wallet.ID)
		if err != nil {
			return err
		}
		if !reserved {
			if err := validateReplay(record, hash, resourceWallet); err != nil {
				return err
			}
			wallet, err = tx.GetWallet(ctx, record.ResourceID)
			if err == nil {
				err = authorizeWallet(wallet, command.OwnerID)
			}
			replayed = true
			return err
		}
		return tx.CreateWallet(ctx, wallet)
	})
	return wallet, replayed, err
}

func (s *Service) GetWallet(ctx context.Context, ownerID, walletID string) (domain.Wallet, error) {
	ownerID = normalizeID(ownerID)
	if ownerID == "" {
		return domain.Wallet{}, domain.ErrInvalidOwner
	}
	wallet, err := s.store.GetWallet(ctx, normalizeID(walletID))
	if err != nil {
		return domain.Wallet{}, err
	}
	if err := authorizeWallet(wallet, ownerID); err != nil {
		return domain.Wallet{}, err
	}
	return wallet, nil
}

func (s *Service) GetTransaction(ctx context.Context, ownerID, transactionID string) (domain.OperationResult, error) {
	ownerID = normalizeID(ownerID)
	if ownerID == "" {
		return domain.OperationResult{}, domain.ErrInvalidOwner
	}
	result, err := s.store.GetOperationResult(ctx, normalizeID(transactionID))
	if err != nil {
		return domain.OperationResult{}, err
	}
	if err := s.authorizeOperation(ctx, ownerID, result); err != nil {
		return domain.OperationResult{}, err
	}
	return result, nil
}

func (s *Service) Deposit(ctx context.Context, command MoneyCommand) (domain.OperationResult, error) {
	return s.changeBalance(ctx, command, domain.TransactionDeposit)
}

func (s *Service) Withdraw(ctx context.Context, command MoneyCommand) (domain.OperationResult, error) {
	return s.changeBalance(ctx, command, domain.TransactionWithdrawal)
}

func (s *Service) changeBalance(ctx context.Context, command MoneyCommand, transactionType domain.TransactionType) (domain.OperationResult, error) {
	command.OwnerID = normalizeID(command.OwnerID)
	command.WalletID = normalizeID(command.WalletID)
	if err := validateMoneyCommand(command); err != nil {
		return domain.OperationResult{}, err
	}
	transactionID, err := s.ids.New()
	if err != nil {
		return domain.OperationResult{}, fmt.Errorf("generate transaction ID: %w", err)
	}
	now := s.clock.Now()
	hash := requestHash(string(transactionType), command.OwnerID, command.WalletID, command.Money.Currency, fmt.Sprint(command.Money.Minor))
	result := domain.OperationResult{}

	err = s.store.WithinTransaction(ctx, func(ctx context.Context, tx TransactionStore) error {
		record, reserved, err := tx.ReserveIdempotency(ctx, command.OwnerID, command.Idempotency.Key, hash, resourceTransaction, transactionID)
		if err != nil {
			return err
		}
		if !reserved {
			if err := validateReplay(record, hash, resourceTransaction); err != nil {
				return err
			}
			result, err = tx.GetOperationResult(ctx, record.ResourceID)
			result.Replayed = true
			return err
		}

		wallet, err := tx.GetWalletForUpdate(ctx, command.WalletID)
		if err != nil {
			return err
		}
		if err := authorizeWallet(wallet, command.OwnerID); err != nil {
			return err
		}
		before := wallet.BalanceMinor
		if transactionType == domain.TransactionDeposit {
			err = wallet.Credit(command.Money, now)
		} else {
			err = wallet.Debit(command.Money, now)
		}
		if err != nil {
			return err
		}

		walletID := wallet.ID
		transaction := domain.FinancialTransaction{
			ID: transactionID, Type: transactionType, AmountMinor: command.Money.Minor,
			Currency: command.Money.Currency, CreatedAt: now,
		}
		direction := domain.EntryCredit
		if transactionType == domain.TransactionDeposit {
			transaction.DestinationWalletID = &walletID
		} else {
			transaction.SourceWalletID = &walletID
			direction = domain.EntryDebit
		}
		if err := tx.CreateFinancialTransaction(ctx, transaction); err != nil {
			return err
		}
		if err := tx.UpdateWallet(ctx, wallet); err != nil {
			return err
		}
		entry, err := tx.CreateLedgerEntry(ctx, domain.LedgerEntry{
			TransactionID: transactionID, WalletID: wallet.ID, Direction: direction,
			AmountMinor: command.Money.Minor, BalanceBeforeMinor: before,
			BalanceAfterMinor: wallet.BalanceMinor, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		result = domain.OperationResult{Transaction: transaction, Entries: []domain.LedgerEntry{entry}}
		return nil
	})
	return result, err
}

func (s *Service) Transfer(ctx context.Context, command TransferCommand) (domain.OperationResult, error) {
	command.OwnerID = normalizeID(command.OwnerID)
	command.SourceWalletID = normalizeID(command.SourceWalletID)
	command.DestinationWalletID = normalizeID(command.DestinationWalletID)
	if command.OwnerID == "" {
		return domain.OperationResult{}, domain.ErrInvalidOwner
	}
	if command.SourceWalletID == command.DestinationWalletID {
		return domain.OperationResult{}, domain.ErrSameWallet
	}
	if command.SourceWalletID == "" || command.DestinationWalletID == "" || command.Money.Minor <= 0 {
		return domain.OperationResult{}, domain.ErrInvalidAmount
	}
	if err := validateIdempotency(command.Idempotency); err != nil {
		return domain.OperationResult{}, err
	}
	transactionID, err := s.ids.New()
	if err != nil {
		return domain.OperationResult{}, fmt.Errorf("generate transaction ID: %w", err)
	}
	now := s.clock.Now()
	hash := requestHash("transfer", command.OwnerID, command.SourceWalletID, command.DestinationWalletID, command.Money.Currency, fmt.Sprint(command.Money.Minor))
	result := domain.OperationResult{}

	err = s.store.WithinTransaction(ctx, func(ctx context.Context, tx TransactionStore) error {
		record, reserved, err := tx.ReserveIdempotency(ctx, command.OwnerID, command.Idempotency.Key, hash, resourceTransaction, transactionID)
		if err != nil {
			return err
		}
		if !reserved {
			if err := validateReplay(record, hash, resourceTransaction); err != nil {
				return err
			}
			result, err = tx.GetOperationResult(ctx, record.ResourceID)
			result.Replayed = true
			return err
		}

		wallets, err := lockWallets(ctx, tx, command.SourceWalletID, command.DestinationWalletID)
		if err != nil {
			return err
		}
		source := wallets[command.SourceWalletID]
		destination := wallets[command.DestinationWalletID]
		if err := authorizeWallet(source, command.OwnerID); err != nil {
			return err
		}
		sourceBefore, destinationBefore := source.BalanceMinor, destination.BalanceMinor
		if err := source.Debit(command.Money, now); err != nil {
			return err
		}
		if err := destination.Credit(command.Money, now); err != nil {
			return err
		}

		sourceID, destinationID := source.ID, destination.ID
		transaction := domain.FinancialTransaction{
			ID: transactionID, Type: domain.TransactionTransfer, AmountMinor: command.Money.Minor,
			Currency: command.Money.Currency, SourceWalletID: &sourceID,
			DestinationWalletID: &destinationID, CreatedAt: now,
		}
		if err := tx.CreateFinancialTransaction(ctx, transaction); err != nil {
			return err
		}
		if err := tx.UpdateWallet(ctx, source); err != nil {
			return err
		}
		if err := tx.UpdateWallet(ctx, destination); err != nil {
			return err
		}
		debit, err := tx.CreateLedgerEntry(ctx, domain.LedgerEntry{
			TransactionID: transactionID, WalletID: source.ID, Direction: domain.EntryDebit,
			AmountMinor: command.Money.Minor, BalanceBeforeMinor: sourceBefore,
			BalanceAfterMinor: source.BalanceMinor, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		credit, err := tx.CreateLedgerEntry(ctx, domain.LedgerEntry{
			TransactionID: transactionID, WalletID: destination.ID, Direction: domain.EntryCredit,
			AmountMinor: command.Money.Minor, BalanceBeforeMinor: destinationBefore,
			BalanceAfterMinor: destination.BalanceMinor, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		result = domain.OperationResult{Transaction: transaction, Entries: []domain.LedgerEntry{debit, credit}}
		return nil
	})
	return result, err
}

func (s *Service) ListHistory(ctx context.Context, ownerID, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
	ownerID = normalizeID(ownerID)
	walletID = normalizeID(walletID)
	if ownerID == "" {
		return domain.HistoryPage{}, domain.ErrInvalidOwner
	}
	if limit <= 0 || limit > 100 {
		return domain.HistoryPage{}, fmt.Errorf("limit must be between 1 and 100")
	}
	wallet, err := s.store.GetWallet(ctx, walletID)
	if err != nil {
		return domain.HistoryPage{}, err
	}
	if err := authorizeWallet(wallet, ownerID); err != nil {
		return domain.HistoryPage{}, err
	}
	return s.store.ListHistory(ctx, walletID, cursor, limit, transactionType)
}

func (s *Service) Ping(ctx context.Context) error { return s.store.Ping(ctx) }

func lockWallets(ctx context.Context, tx TransactionStore, firstID, secondID string) (map[string]domain.Wallet, error) {
	// A stable lock order prevents opposing transfers from forming a lock cycle.
	ids := []string{firstID, secondID}
	sort.Strings(ids)
	wallets := make(map[string]domain.Wallet, 2)
	for _, id := range ids {
		wallet, err := tx.GetWalletForUpdate(ctx, id)
		if err != nil {
			return nil, err
		}
		wallets[id] = wallet
	}
	return wallets, nil
}

func validateMoneyCommand(command MoneyCommand) error {
	if command.OwnerID == "" {
		return domain.ErrInvalidOwner
	}
	if command.WalletID == "" || command.Money.Minor <= 0 {
		return domain.ErrInvalidAmount
	}
	return validateIdempotency(command.Idempotency)
}

func validateIdempotency(value Idempotency) error {
	if strings.TrimSpace(value.Key) == "" {
		return errors.New("idempotency key is required")
	}
	return nil
}

func authorizeWallet(wallet domain.Wallet, ownerID string) error {
	if ownerID == "" {
		return domain.ErrInvalidOwner
	}
	if wallet.OwnerID != ownerID {
		return domain.ErrWalletAccessDenied
	}
	return nil
}

func (s *Service) authorizeOperation(ctx context.Context, ownerID string, result domain.OperationResult) error {
	if ownerID == "" {
		return domain.ErrInvalidOwner
	}
	ids := make(map[string]struct{}, 2)
	if result.Transaction.SourceWalletID != nil {
		ids[*result.Transaction.SourceWalletID] = struct{}{}
	}
	if result.Transaction.DestinationWalletID != nil {
		ids[*result.Transaction.DestinationWalletID] = struct{}{}
	}
	for walletID := range ids {
		wallet, err := s.store.GetWallet(ctx, walletID)
		if err != nil {
			return err
		}
		if wallet.OwnerID == ownerID {
			return nil
		}
	}
	return domain.ErrWalletAccessDenied
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateReplay(record IdempotencyRecord, requestHash, resourceType string) error {
	if record.RequestHash != requestHash || record.ResourceType != resourceType {
		return domain.ErrIdempotencyConflict
	}
	return nil
}

func requestHash(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", digest)
}
