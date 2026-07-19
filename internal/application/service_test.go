package application

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"testing"
	"time"

	"walletservice/internal/domain"
)

var errInjected = errors.New("injected persistence failure")

const testOwnerID = "owner-1"

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

type sequenceIDs struct {
	values []string
	err    error
	index  int
}

func (generator *sequenceIDs) New() (string, error) {
	if generator.err != nil {
		return "", generator.err
	}
	value := generator.values[generator.index]
	generator.index++
	return value, nil
}

type memoryState struct {
	wallets      map[string]domain.Wallet
	transactions map[string]domain.FinancialTransaction
	entries      map[string][]domain.LedgerEntry
	idempotency  map[string]IdempotencyRecord
	sequence     int64
}

func newMemoryState() memoryState {
	return memoryState{
		wallets: make(map[string]domain.Wallet), transactions: make(map[string]domain.FinancialTransaction),
		entries: make(map[string][]domain.LedgerEntry), idempotency: make(map[string]IdempotencyRecord),
	}
}

func (state memoryState) clone() memoryState {
	cloned := newMemoryState()
	cloned.sequence = state.sequence
	for id, wallet := range state.wallets {
		cloned.wallets[id] = wallet
	}
	for id, transaction := range state.transactions {
		cloned.transactions[id] = transaction
	}
	for id, entries := range state.entries {
		cloned.entries[id] = append([]domain.LedgerEntry(nil), entries...)
	}
	for key, record := range state.idempotency {
		cloned.idempotency[key] = record
	}
	return cloned
}

type memoryStore struct {
	mu         sync.Mutex
	state      memoryState
	failMethod string
	lockOrder  []string
}

func newMemoryStore(wallets ...domain.Wallet) *memoryStore {
	store := &memoryStore{state: newMemoryState()}
	for _, wallet := range wallets {
		store.state.wallets[wallet.ID] = wallet
	}
	return store
}

func (store *memoryStore) WithinTransaction(ctx context.Context, operation func(context.Context, TransactionStore) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	working := store.state.clone()
	tx := &memoryTransaction{state: &working, store: store}
	if err := operation(ctx, tx); err != nil {
		return err
	}
	store.state = working
	return nil
}

func (store *memoryStore) GetWallet(ctx context.Context, walletID string) (domain.Wallet, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Wallet{}, err
	}
	wallet, exists := store.state.wallets[walletID]
	if !exists {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	return wallet, nil
}

func (store *memoryStore) GetOperationResult(ctx context.Context, transactionID string) (domain.OperationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return operationResult(store.state, transactionID)
}

func (store *memoryStore) ListHistory(_ context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	items := make([]domain.HistoryItem, 0)
	for transactionID, entries := range store.state.entries {
		transaction := store.state.transactions[transactionID]
		if transactionType != nil && transaction.Type != *transactionType {
			continue
		}
		for _, entry := range entries {
			if entry.WalletID != walletID || (cursor > 0 && entry.Sequence >= cursor) {
				continue
			}
			items = append(items, domain.HistoryItem{Sequence: entry.Sequence, TransactionID: transactionID, Type: transaction.Type})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sequence > items[j].Sequence })
	page := domain.HistoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].Sequence
	}
	return page, nil
}

func (store *memoryStore) Ping(ctx context.Context) error { return ctx.Err() }

type memoryTransaction struct {
	state *memoryState
	store *memoryStore
}

func (tx *memoryTransaction) fail(method string) error {
	if tx.store.failMethod == method {
		return errInjected
	}
	return nil
}

func (tx *memoryTransaction) ReserveIdempotency(_ context.Context, scope, key, requestHash, resourceType, resourceID string) (IdempotencyRecord, bool, error) {
	if err := tx.fail("ReserveIdempotency"); err != nil {
		return IdempotencyRecord{}, false, err
	}
	mapKey := scope + "\x00" + key
	if record, exists := tx.state.idempotency[mapKey]; exists {
		return record, false, nil
	}
	record := IdempotencyRecord{RequestHash: requestHash, ResourceType: resourceType, ResourceID: resourceID}
	tx.state.idempotency[mapKey] = record
	return record, true, nil
}

func (tx *memoryTransaction) CreateWallet(_ context.Context, wallet domain.Wallet) error {
	if err := tx.fail("CreateWallet"); err != nil {
		return err
	}
	for _, existing := range tx.state.wallets {
		if existing.OwnerID == wallet.OwnerID {
			return domain.ErrOwnerHasWallet
		}
	}
	tx.state.wallets[wallet.ID] = wallet
	return nil
}

func (tx *memoryTransaction) GetWallet(_ context.Context, walletID string) (domain.Wallet, error) {
	wallet, exists := tx.state.wallets[walletID]
	if !exists {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	return wallet, nil
}

func (tx *memoryTransaction) GetWalletForUpdate(ctx context.Context, walletID string) (domain.Wallet, error) {
	if err := tx.fail("GetWalletForUpdate"); err != nil {
		return domain.Wallet{}, err
	}
	tx.store.lockOrder = append(tx.store.lockOrder, walletID)
	return tx.GetWallet(ctx, walletID)
}

func (tx *memoryTransaction) UpdateWallet(_ context.Context, wallet domain.Wallet) error {
	if err := tx.fail("UpdateWallet"); err != nil {
		return err
	}
	if _, exists := tx.state.wallets[wallet.ID]; !exists {
		return domain.ErrWalletNotFound
	}
	tx.state.wallets[wallet.ID] = wallet
	return nil
}

func (tx *memoryTransaction) CreateFinancialTransaction(_ context.Context, transaction domain.FinancialTransaction) error {
	if err := tx.fail("CreateFinancialTransaction"); err != nil {
		return err
	}
	tx.state.transactions[transaction.ID] = transaction
	return nil
}

func (tx *memoryTransaction) CreateLedgerEntry(_ context.Context, entry domain.LedgerEntry) (domain.LedgerEntry, error) {
	if err := tx.fail("CreateLedgerEntry"); err != nil {
		return domain.LedgerEntry{}, err
	}
	tx.state.sequence++
	entry.Sequence = tx.state.sequence
	tx.state.entries[entry.TransactionID] = append(tx.state.entries[entry.TransactionID], entry)
	return entry, nil
}

func (tx *memoryTransaction) GetOperationResult(_ context.Context, transactionID string) (domain.OperationResult, error) {
	return operationResult(*tx.state, transactionID)
}

func operationResult(state memoryState, transactionID string) (domain.OperationResult, error) {
	transaction, exists := state.transactions[transactionID]
	if !exists {
		return domain.OperationResult{}, domain.ErrTransactionNotFound
	}
	return domain.OperationResult{Transaction: transaction, Entries: append([]domain.LedgerEntry(nil), state.entries[transactionID]...)}, nil
}

func TestCreateWalletAndIdempotentReplay(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	service := newTestService(store, "wallet-1", "unused-id")
	command := CreateWalletCommand{OwnerID: testOwnerID, Currency: "usd", Idempotency: testIdempotency("create")}

	created, replayed, err := service.CreateWallet(context.Background(), command)
	if err != nil || replayed {
		t.Fatalf("create wallet: replayed=%v err=%v", replayed, err)
	}
	if created.Currency != "USD" || created.BalanceMinor != 0 || created.Status != domain.WalletActive {
		t.Fatalf("unexpected wallet: %+v", created)
	}
	replayedWallet, replayed, err := service.CreateWallet(context.Background(), command)
	if err != nil || !replayed {
		t.Fatalf("replay wallet: replayed=%v err=%v", replayed, err)
	}
	if replayedWallet.ID != created.ID || len(store.state.wallets) != 1 {
		t.Fatalf("replay created another wallet: %+v", replayedWallet)
	}
}

func TestOwnerCanHaveOnlyOneWallet(t *testing.T) {
	store := newMemoryStore()
	service := newTestService(store, "wallet-1", "wallet-2")

	first, _, err := service.CreateWallet(context.Background(), CreateWalletCommand{
		OwnerID: testOwnerID, Currency: "USD", Idempotency: testIdempotency("first"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.CreateWallet(context.Background(), CreateWalletCommand{
		OwnerID: testOwnerID, Currency: "USD", Idempotency: testIdempotency("second"),
	})
	if !errors.Is(err, domain.ErrOwnerHasWallet) {
		t.Fatalf("expected owner wallet conflict, got %v", err)
	}
	if len(store.state.wallets) != 1 || store.state.wallets[first.ID].OwnerID != testOwnerID {
		t.Fatalf("owner uniqueness failure changed state: %+v", store.state.wallets)
	}
}

func TestWalletOwnershipIsEnforced(t *testing.T) {
	store := newMemoryStore(testWallet("wallet-1", "USD", 100), testWallet("wallet-2", "USD", 100))
	service := newTestService(store, "transaction-1", "transaction-2", "transaction-3")
	otherOwner := "owner-2"

	if _, err := service.GetWallet(context.Background(), otherOwner, "wallet-1"); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected get-wallet access denial, got %v", err)
	}
	if _, err := service.ListHistory(context.Background(), otherOwner, "wallet-1", 0, 10, nil); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected history access denial, got %v", err)
	}
	money := domain.Money{Minor: 10, Currency: "USD"}
	if _, err := service.Withdraw(context.Background(), MoneyCommand{
		OwnerID: otherOwner, WalletID: "wallet-1", Money: money, Idempotency: testIdempotency("withdraw"),
	}); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected withdrawal access denial, got %v", err)
	}
	if _, err := service.Deposit(context.Background(), MoneyCommand{
		OwnerID: otherOwner, WalletID: "wallet-1", Money: money, Idempotency: testIdempotency("deposit"),
	}); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected deposit access denial, got %v", err)
	}
	if _, err := service.Transfer(context.Background(), TransferCommand{
		OwnerID: otherOwner, SourceWalletID: "wallet-1", DestinationWalletID: "wallet-2", Money: money,
		Idempotency: testIdempotency("transfer"),
	}); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected transfer access denial, got %v", err)
	}
	assertUnchangedWallet(t, store, "wallet-1", 100)
}

func TestTransactionIsVisibleOnlyToParticipatingOwner(t *testing.T) {
	store := newMemoryStore(testWallet("wallet-1", "USD", 100))
	service := newTestService(store, "transaction-1")
	result, err := service.Deposit(context.Background(), MoneyCommand{
		OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: 10, Currency: "USD"},
		Idempotency: testIdempotency("deposit"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetTransaction(context.Background(), testOwnerID, result.Transaction.ID); err != nil {
		t.Fatalf("participating owner could not read transaction: %v", err)
	}
	if _, err := service.GetTransaction(context.Background(), "owner-2", result.Transaction.ID); !errors.Is(err, domain.ErrWalletAccessDenied) {
		t.Fatalf("expected transaction access denial, got %v", err)
	}
}

func TestBalanceOperations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		initial       int64
		operation     domain.TransactionType
		amount        int64
		wantBalance   int64
		wantDirection domain.EntryDirection
	}{
		{name: "deposit", initial: 500, operation: domain.TransactionDeposit, amount: 250, wantBalance: 750, wantDirection: domain.EntryCredit},
		{name: "withdraw", initial: 500, operation: domain.TransactionWithdrawal, amount: 250, wantBalance: 250, wantDirection: domain.EntryDebit},
		{name: "withdraw exact balance", initial: 500, operation: domain.TransactionWithdrawal, amount: 500, wantBalance: 0, wantDirection: domain.EntryDebit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore(testWallet("wallet-1", "USD", test.initial))
			service := newTestService(store, "transaction-1")
			command := MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: test.amount, Currency: "USD"}, Idempotency: testIdempotency(test.name)}
			var result domain.OperationResult
			var err error
			if test.operation == domain.TransactionDeposit {
				result, err = service.Deposit(context.Background(), command)
			} else {
				result, err = service.Withdraw(context.Background(), command)
			}
			if err != nil {
				t.Fatal(err)
			}
			wallet, _ := store.GetWallet(context.Background(), "wallet-1")
			if wallet.BalanceMinor != test.wantBalance {
				t.Fatalf("expected balance %d, got %d", test.wantBalance, wallet.BalanceMinor)
			}
			if len(result.Entries) != 1 || result.Entries[0].Direction != test.wantDirection || result.Entries[0].BalanceAfterMinor != test.wantBalance {
				t.Fatalf("unexpected ledger entry: %+v", result.Entries)
			}
		})
	}
}

func TestInvalidBalanceOperationsDoNotMutateState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		money     domain.Money
		operation domain.TransactionType
		wantErr   error
	}{
		{name: "zero deposit", money: domain.Money{Minor: 0, Currency: "USD"}, operation: domain.TransactionDeposit, wantErr: domain.ErrInvalidAmount},
		{name: "negative deposit", money: domain.Money{Minor: -1, Currency: "USD"}, operation: domain.TransactionDeposit, wantErr: domain.ErrInvalidAmount},
		{name: "negative withdrawal", money: domain.Money{Minor: -1, Currency: "USD"}, operation: domain.TransactionWithdrawal, wantErr: domain.ErrInvalidAmount},
		{name: "insufficient balance", money: domain.Money{Minor: 101, Currency: "USD"}, operation: domain.TransactionWithdrawal, wantErr: domain.ErrInsufficientFunds},
		{name: "currency mismatch", money: domain.Money{Minor: 50, Currency: "EUR"}, operation: domain.TransactionWithdrawal, wantErr: domain.ErrCurrencyMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore(testWallet("wallet-1", "USD", 100))
			service := newTestService(store, "transaction-1")
			command := MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: test.money, Idempotency: testIdempotency(test.name)}
			var err error
			if test.operation == domain.TransactionDeposit {
				_, err = service.Deposit(context.Background(), command)
			} else {
				_, err = service.Withdraw(context.Background(), command)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			assertUnchangedWallet(t, store, "wallet-1", 100)
			if len(store.state.transactions) != 0 || len(store.state.idempotency) != 0 {
				t.Fatal("failed operation persisted transaction or idempotency state")
			}
		})
	}
}

func TestTransferIsAtomicAndLocksWalletsInStableOrder(t *testing.T) {
	t.Parallel()
	store := newMemoryStore(testWallet("wallet-b", "USD", 100), testWallet("wallet-a", "USD", 300))
	service := newTestService(store, "transaction-1")
	result, err := service.Transfer(context.Background(), TransferCommand{
		OwnerID:        testOwnerID,
		SourceWalletID: "wallet-b", DestinationWalletID: "wallet-a",
		Money: domain.Money{Minor: 75, Currency: "USD"}, Idempotency: testIdempotency("transfer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.lockOrder; len(got) != 2 || got[0] != "wallet-a" || got[1] != "wallet-b" {
		t.Fatalf("wallets were not locked in stable order: %v", got)
	}
	assertUnchangedWallet(t, store, "wallet-b", 25)
	assertUnchangedWallet(t, store, "wallet-a", 375)
	if len(result.Entries) != 2 || result.Entries[0].Direction != domain.EntryDebit || result.Entries[1].Direction != domain.EntryCredit {
		t.Fatalf("unexpected transfer entries: %+v", result.Entries)
	}
}

func TestInvalidTransfersDoNotMutateWallets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      domain.Wallet
		destination domain.Wallet
		command     TransferCommand
		wantErr     error
	}{
		{
			name: "same wallet", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "USD", 100),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "wallet-a", Money: domain.Money{Minor: 1, Currency: "USD"}, Idempotency: testIdempotency("same")}, wantErr: domain.ErrSameWallet,
		},
		{
			name: "negative amount", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "USD", 100),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "wallet-b", Money: domain.Money{Minor: -1, Currency: "USD"}, Idempotency: testIdempotency("negative")}, wantErr: domain.ErrInvalidAmount,
		},
		{
			name: "insufficient balance", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "USD", 100),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "wallet-b", Money: domain.Money{Minor: 101, Currency: "USD"}, Idempotency: testIdempotency("insufficient")}, wantErr: domain.ErrInsufficientFunds,
		},
		{
			name: "destination currency mismatch", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "EUR", 100),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "wallet-b", Money: domain.Money{Minor: 10, Currency: "USD"}, Idempotency: testIdempotency("currency")}, wantErr: domain.ErrCurrencyMismatch,
		},
		{
			name: "destination overflow", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "USD", math.MaxInt64),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "wallet-b", Money: domain.Money{Minor: 1, Currency: "USD"}, Idempotency: testIdempotency("overflow")}, wantErr: domain.ErrAmountOverflow,
		},
		{
			name: "missing destination", source: testWallet("wallet-a", "USD", 100), destination: testWallet("wallet-b", "USD", 100),
			command: TransferCommand{OwnerID: testOwnerID, SourceWalletID: "wallet-a", DestinationWalletID: "missing", Money: domain.Money{Minor: 1, Currency: "USD"}, Idempotency: testIdempotency("missing")}, wantErr: domain.ErrWalletNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore(test.source, test.destination)
			service := newTestService(store, "transaction-1")
			_, err := service.Transfer(context.Background(), test.command)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			assertUnchangedWallet(t, store, test.source.ID, test.source.BalanceMinor)
			assertUnchangedWallet(t, store, test.destination.ID, test.destination.BalanceMinor)
			if len(store.state.transactions) != 0 || len(store.state.entries) != 0 {
				t.Fatal("invalid transfer persisted financial state")
			}
		})
	}
}

func TestIdempotentDepositIsAppliedOnce(t *testing.T) {
	t.Parallel()
	store := newMemoryStore(testWallet("wallet-1", "USD", 100))
	service := newTestService(store, "transaction-1", "unused-transaction")
	command := MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: 50, Currency: "USD"}, Idempotency: testIdempotency("deposit")}
	first, err := service.Deposit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Deposit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.Transaction.ID != second.Transaction.ID {
		t.Fatalf("expected replay of %s, got %+v", first.Transaction.ID, second)
	}
	assertUnchangedWallet(t, store, "wallet-1", 150)
	if len(store.state.transactions) != 1 {
		t.Fatalf("expected one transaction, got %d", len(store.state.transactions))
	}
}

func TestIdempotencyKeyCannotBeReusedWithDifferentPayload(t *testing.T) {
	t.Parallel()
	store := newMemoryStore(testWallet("wallet-1", "USD", 100))
	service := newTestService(store, "transaction-1", "unused-transaction")
	idempotency := testIdempotency("deposit")
	if _, err := service.Deposit(context.Background(), MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: 50, Currency: "USD"}, Idempotency: idempotency}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Deposit(context.Background(), MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: 51, Currency: "USD"}, Idempotency: idempotency})
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	assertUnchangedWallet(t, store, "wallet-1", 150)
}

func TestPersistenceFailuresRollBackEntireOperation(t *testing.T) {
	t.Parallel()
	for _, failMethod := range []string{"CreateFinancialTransaction", "UpdateWallet", "CreateLedgerEntry"} {
		t.Run(failMethod, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore(testWallet("wallet-a", "USD", 100), testWallet("wallet-b", "USD", 50))
			store.failMethod = failMethod
			service := newTestService(store, "transaction-1")
			_, err := service.Transfer(context.Background(), TransferCommand{
				OwnerID:        testOwnerID,
				SourceWalletID: "wallet-a", DestinationWalletID: "wallet-b",
				Money: domain.Money{Minor: 25, Currency: "USD"}, Idempotency: testIdempotency(failMethod),
			})
			if !errors.Is(err, errInjected) {
				t.Fatalf("expected injected error, got %v", err)
			}
			assertUnchangedWallet(t, store, "wallet-a", 100)
			assertUnchangedWallet(t, store, "wallet-b", 50)
			if len(store.state.transactions) != 0 || len(store.state.entries) != 0 || len(store.state.idempotency) != 0 {
				t.Fatal("rolled-back transaction left persisted state")
			}
		})
	}
}

func TestCancelledContextStopsBeforeTransaction(t *testing.T) {
	t.Parallel()
	store := newMemoryStore(testWallet("wallet-1", "USD", 100))
	service := newTestService(store, "transaction-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Deposit(ctx, MoneyCommand{OwnerID: testOwnerID, WalletID: "wallet-1", Money: domain.Money{Minor: 1, Currency: "USD"}, Idempotency: testIdempotency("cancelled")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	assertUnchangedWallet(t, store, "wallet-1", 100)
}

func TestListHistoryValidatesLimit(t *testing.T) {
	t.Parallel()
	service := newTestService(newMemoryStore(testWallet("wallet-1", "USD", 0)), "unused")
	for _, limit := range []int{-1, 0, 101} {
		if _, err := service.ListHistory(context.Background(), testOwnerID, "wallet-1", 0, limit, nil); err == nil {
			t.Fatalf("expected limit %d to be rejected", limit)
		}
	}
}

func TestCreateWalletInputAndDependencyFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command CreateWalletCommand
		ids     *sequenceIDs
		wantErr error
	}{
		{name: "unsupported currency", command: CreateWalletCommand{OwnerID: testOwnerID, Currency: "BTC", Idempotency: testIdempotency("key")}, ids: &sequenceIDs{values: []string{"wallet"}}, wantErr: domain.ErrInvalidCurrency},
		{name: "missing idempotency", command: CreateWalletCommand{OwnerID: testOwnerID, Currency: "USD"}, ids: &sequenceIDs{values: []string{"wallet"}}, wantErr: errors.New("idempotency")},
		{name: "ID generator failure", command: CreateWalletCommand{OwnerID: testOwnerID, Currency: "USD", Idempotency: testIdempotency("key")}, ids: &sequenceIDs{err: errInjected}, wantErr: errInjected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newMemoryStore()
			service := NewService(store, test.ids, fixedClock{value: time.Now()})
			_, _, err := service.CreateWallet(context.Background(), test.command)
			if test.name == "missing idempotency" {
				if err == nil {
					t.Fatal("expected missing idempotency error")
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			if len(store.state.wallets) != 0 {
				t.Fatal("invalid create persisted a wallet")
			}
		})
	}
}

func newTestService(store Store, ids ...string) *Service {
	return NewService(store, &sequenceIDs{values: ids}, fixedClock{value: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)})
}

func testWallet(id, currency string, balance int64) domain.Wallet {
	return domain.Wallet{ID: id, OwnerID: testOwnerID, Currency: currency, BalanceMinor: balance, Status: domain.WalletActive, Version: 1}
}

func testIdempotency(key string) Idempotency { return Idempotency{Key: key} }

func assertUnchangedWallet(t *testing.T, store *memoryStore, walletID string, wantBalance int64) {
	t.Helper()
	wallet, err := store.GetWallet(context.Background(), walletID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceMinor != wantBalance {
		t.Fatalf("wallet %s: expected balance %d, got %d", walletID, wantBalance, wallet.BalanceMinor)
	}
}

var _ Store = (*memoryStore)(nil)
var _ TransactionStore = (*memoryTransaction)(nil)
