package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"walletservice/internal/application"
	"walletservice/internal/domain"
	"walletservice/internal/platform/identity"
)

const (
	testWalletID      = "11111111-1111-4111-8111-111111111111"
	testDestinationID = "22222222-2222-4222-8222-222222222222"
	testTransactionID = "33333333-3333-4333-8333-333333333333"
)

type stubService struct {
	createWallet   func(context.Context, application.CreateWalletCommand) (domain.Wallet, bool, error)
	getWallet      func(context.Context, string) (domain.Wallet, error)
	deposit        func(context.Context, application.MoneyCommand) (domain.OperationResult, error)
	withdraw       func(context.Context, application.MoneyCommand) (domain.OperationResult, error)
	transfer       func(context.Context, application.TransferCommand) (domain.OperationResult, error)
	getTransaction func(context.Context, string) (domain.OperationResult, error)
	listHistory    func(context.Context, string, int64, int, *domain.TransactionType) (domain.HistoryPage, error)
	ping           func(context.Context) error
}

func (stub *stubService) CreateWallet(ctx context.Context, command application.CreateWalletCommand) (domain.Wallet, bool, error) {
	return stub.createWallet(ctx, command)
}

func (stub *stubService) GetWallet(ctx context.Context, walletID string) (domain.Wallet, error) {
	return stub.getWallet(ctx, walletID)
}

func (stub *stubService) Deposit(ctx context.Context, command application.MoneyCommand) (domain.OperationResult, error) {
	return stub.deposit(ctx, command)
}

func (stub *stubService) Withdraw(ctx context.Context, command application.MoneyCommand) (domain.OperationResult, error) {
	return stub.withdraw(ctx, command)
}

func (stub *stubService) Transfer(ctx context.Context, command application.TransferCommand) (domain.OperationResult, error) {
	return stub.transfer(ctx, command)
}

func (stub *stubService) GetTransaction(ctx context.Context, transactionID string) (domain.OperationResult, error) {
	return stub.getTransaction(ctx, transactionID)
}

func (stub *stubService) ListHistory(ctx context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
	return stub.listHistory(ctx, walletID, cursor, limit, transactionType)
}

func (stub *stubService) Ping(ctx context.Context) error { return stub.ping(ctx) }

func TestInvalidWalletIDReturnsStructuredProblem(t *testing.T) {
	t.Parallel()
	handler := testHandler()
	request := httptest.NewRequest(http.MethodGet, "/v1/wallets/not-a-uuid", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("expected problem content type, got %q", contentType)
	}
	if !strings.Contains(response.Body.String(), `"code":"INVALID_REQUEST"`) {
		t.Fatalf("response did not contain stable error code: %s", response.Body.String())
	}
}

func TestUnsupportedMediaTypeReturns415(t *testing.T) {
	t.Parallel()
	handler := testHandler()
	request := httptest.NewRequest(http.MethodPost, "/v1/wallets", strings.NewReader(`{"currency":"USD"}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"code":"UNSUPPORTED_MEDIA_TYPE"`) {
		t.Fatalf("response did not contain media type error code: %s", response.Body.String())
	}
}

func TestUnsupportedMethodReturnsStructured405(t *testing.T) {
	t.Parallel()
	handler := testHandler()
	request := httptest.NewRequest(http.MethodDelete, "/v1/wallets", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("expected Allow header to be POST, got %q", response.Header().Get("Allow"))
	}
}

func TestCreateWalletResponses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		replayed   bool
		wantStatus int
	}{
		{name: "new wallet", wantStatus: http.StatusCreated},
		{name: "idempotent replay", replayed: true, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := defaultStubService()
			service.createWallet = func(_ context.Context, command application.CreateWalletCommand) (domain.Wallet, bool, error) {
				if command.Currency != "USD" || command.Idempotency.Key != "create-key" || command.Idempotency.Scope != "client-1" {
					t.Fatalf("unexpected command: %+v", command)
				}
				return testWallet(), test.replayed, nil
			}
			response := performJSONRequest(testAPIHandler(service), http.MethodPost, "/v1/wallets", `{"currency":"USD"}`, "create-key")
			if response.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			if response.Header().Get("Location") != "/v1/wallets/"+testWalletID {
				t.Fatalf("unexpected Location: %s", response.Header().Get("Location"))
			}
			assertJSONContains(t, response, `"balance":"10.00"`, `"currency":"USD"`)
		})
	}
}

func TestDepositAndWithdrawalRequests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		path string
		kind domain.TransactionType
	}{
		{name: "deposit", path: "/v1/wallets/" + testWalletID + "/deposits", kind: domain.TransactionDeposit},
		{name: "withdrawal", path: "/v1/wallets/" + testWalletID + "/withdrawals", kind: domain.TransactionWithdrawal},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := defaultStubService()
			operation := func(_ context.Context, command application.MoneyCommand) (domain.OperationResult, error) {
				if command.WalletID != testWalletID || command.Money.Minor != 1234 || command.Money.Currency != "USD" {
					t.Fatalf("unexpected command: %+v", command)
				}
				return testOperation(test.kind), nil
			}
			if test.kind == domain.TransactionDeposit {
				service.deposit = operation
			} else {
				service.withdraw = operation
			}
			response := performJSONRequest(testAPIHandler(service), http.MethodPost, test.path, `{"amount":"12.34","currency":"USD"}`, "money-key")
			if response.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Location") != "/v1/transactions/"+testTransactionID {
				t.Fatalf("unexpected Location: %s", response.Header().Get("Location"))
			}
			assertJSONContains(t, response, `"amount":"12.34"`, `"id":"`+testTransactionID+`"`)
		})
	}
}

func TestTransferRequest(t *testing.T) {
	t.Parallel()
	service := defaultStubService()
	service.transfer = func(_ context.Context, command application.TransferCommand) (domain.OperationResult, error) {
		if command.SourceWalletID != testWalletID || command.DestinationWalletID != testDestinationID || command.Money.Minor != 2500 {
			t.Fatalf("unexpected command: %+v", command)
		}
		return testOperation(domain.TransactionTransfer), nil
	}
	body := `{"sourceWalletId":"` + testWalletID + `","destinationWalletId":"` + testDestinationID + `","amount":"25.00","currency":"USD"}`
	response := performJSONRequest(testAPIHandler(service), http.MethodPost, "/v1/transfers", body, "transfer-key")
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	assertJSONContains(t, response, `"type":"TRANSFER"`, `"sourceWalletId":"`+testWalletID+`"`)
}

func TestReadEndpoints(t *testing.T) {
	t.Parallel()
	service := defaultStubService()
	service.getWallet = func(_ context.Context, walletID string) (domain.Wallet, error) {
		if walletID != testWalletID {
			t.Fatalf("unexpected wallet ID: %s", walletID)
		}
		return testWallet(), nil
	}
	service.getTransaction = func(_ context.Context, transactionID string) (domain.OperationResult, error) {
		if transactionID != testTransactionID {
			t.Fatalf("unexpected transaction ID: %s", transactionID)
		}
		return testOperation(domain.TransactionDeposit), nil
	}
	service.listHistory = func(_ context.Context, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error) {
		if walletID != testWalletID || cursor != 42 || limit != 10 || transactionType == nil || *transactionType != domain.TransactionTransfer {
			t.Fatalf("unexpected history query: wallet=%s cursor=%d limit=%d type=%v", walletID, cursor, limit, transactionType)
		}
		return domain.HistoryPage{Items: []domain.HistoryItem{{
			Sequence: 41, TransactionID: testTransactionID, Type: domain.TransactionTransfer,
			Direction: domain.EntryDebit, AmountMinor: 2500, Currency: "USD", BalanceAfterMinor: 7500,
			CreatedAt: testTime(),
		}}, NextCursor: 41}, nil
	}

	tests := []struct {
		name     string
		path     string
		contains string
	}{
		{name: "wallet", path: "/v1/wallets/" + testWalletID, contains: `"balance":"10.00"`},
		{name: "transaction", path: "/v1/transactions/" + testTransactionID, contains: `"id":"` + testTransactionID + `"`},
		{name: "history", path: "/v1/wallets/" + testWalletID + "/transactions?limit=10&cursor=NDI&type=transfer", contains: `"nextCursor":"NDE"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(testAPIHandler(service), http.MethodGet, test.path, "", "", "")
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
			}
			assertJSONContains(t, response, test.contains)
		})
	}
}

func TestDomainErrorsMapToProblemResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: domain.ErrWalletNotFound, wantStatus: 404, wantCode: "RESOURCE_NOT_FOUND"},
		{name: "invalid amount", err: domain.ErrInvalidAmount, wantStatus: 422, wantCode: "INVALID_AMOUNT"},
		{name: "invalid currency", err: domain.ErrInvalidCurrency, wantStatus: 422, wantCode: "INVALID_CURRENCY"},
		{name: "currency mismatch", err: domain.ErrCurrencyMismatch, wantStatus: 422, wantCode: "CURRENCY_MISMATCH"},
		{name: "insufficient funds", err: domain.ErrInsufficientFunds, wantStatus: 422, wantCode: "INSUFFICIENT_FUNDS"},
		{name: "same wallet", err: domain.ErrSameWallet, wantStatus: 422, wantCode: "SAME_WALLET_TRANSFER"},
		{name: "inactive wallet", err: domain.ErrWalletNotActive, wantStatus: 409, wantCode: "WALLET_NOT_ACTIVE"},
		{name: "idempotency conflict", err: domain.ErrIdempotencyConflict, wantStatus: 409, wantCode: "IDEMPOTENCY_CONFLICT"},
		{name: "overflow", err: domain.ErrAmountOverflow, wantStatus: 422, wantCode: "AMOUNT_OUT_OF_RANGE"},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: 500, wantCode: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := defaultStubService()
			service.getWallet = func(context.Context, string) (domain.Wallet, error) { return domain.Wallet{}, test.err }
			response := performRequest(testAPIHandler(service), http.MethodGet, "/v1/wallets/"+testWalletID, "", "", "")
			if response.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			assertJSONContains(t, response, `"code":"`+test.wantCode+`"`, `"requestId":`)
		})
	}
}

func TestMalformedCreateWalletRequests(t *testing.T) {
	t.Parallel()
	oversized := `{"currency":"USD","padding":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`
	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{name: "empty body", body: "", contentType: "application/json", wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "malformed JSON", body: `{"currency":`, contentType: "application/json", wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "unknown field", body: `{"currency":"USD","extra":true}`, contentType: "application/json", wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "wrong field type", body: `{"currency":123}`, contentType: "application/json", wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "multiple objects", body: `{"currency":"USD"}{"currency":"EUR"}`, contentType: "application/json", wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "missing content type", body: `{"currency":"USD"}`, wantStatus: 415, wantCode: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "oversized body", body: oversized, contentType: "application/json", wantStatus: 413, wantCode: "REQUEST_TOO_LARGE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := performRequest(testHandler(), http.MethodPost, "/v1/wallets", test.body, test.contentType, "key")
			if response.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			assertJSONContains(t, response, `"code":"`+test.wantCode+`"`)
		})
	}
}

func TestInvalidMoneyRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "zero", body: `{"amount":"0.00","currency":"USD"}`, wantStatus: 422, wantCode: "INVALID_AMOUNT"},
		{name: "negative", body: `{"amount":"-1.00","currency":"USD"}`, wantStatus: 422, wantCode: "INVALID_AMOUNT"},
		{name: "too precise", body: `{"amount":"1.001","currency":"USD"}`, wantStatus: 422, wantCode: "INVALID_AMOUNT"},
		{name: "not a number", body: `{"amount":"abc","currency":"USD"}`, wantStatus: 422, wantCode: "INVALID_AMOUNT"},
		{name: "numeric JSON amount", body: `{"amount":-1,"currency":"USD"}`, wantStatus: 400, wantCode: "INVALID_REQUEST"},
		{name: "unsupported currency", body: `{"amount":"1.00","currency":"BTC"}`, wantStatus: 422, wantCode: "INVALID_CURRENCY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := performJSONRequest(testHandler(), http.MethodPost, "/v1/wallets/"+testWalletID+"/deposits", test.body, "key")
			if response.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			assertJSONContains(t, response, `"code":"`+test.wantCode+`"`)
		})
	}
}

func TestFinancialBusinessErrorsUseFinancialEndpoints(t *testing.T) {
	t.Parallel()
	t.Run("insufficient withdrawal", func(t *testing.T) {
		service := defaultStubService()
		service.withdraw = func(context.Context, application.MoneyCommand) (domain.OperationResult, error) {
			return domain.OperationResult{}, domain.ErrInsufficientFunds
		}
		response := performJSONRequest(testAPIHandler(service), http.MethodPost, "/v1/wallets/"+testWalletID+"/withdrawals", `{"amount":"100.00","currency":"USD"}`, "withdraw")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
		}
		assertJSONContains(t, response, `"code":"INSUFFICIENT_FUNDS"`)
	})
	t.Run("same wallet transfer", func(t *testing.T) {
		service := defaultStubService()
		service.transfer = func(context.Context, application.TransferCommand) (domain.OperationResult, error) {
			return domain.OperationResult{}, domain.ErrSameWallet
		}
		body := `{"sourceWalletId":"` + testWalletID + `","destinationWalletId":"` + testWalletID + `","amount":"1.00","currency":"USD"}`
		response := performJSONRequest(testAPIHandler(service), http.MethodPost, "/v1/transfers", body, "transfer")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
		}
		assertJSONContains(t, response, `"code":"SAME_WALLET_TRANSFER"`)
	})
}

func TestRequestValidationEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		key    string
	}{
		{name: "missing idempotency key", method: http.MethodPost, path: "/v1/wallets", body: `{"currency":"USD"}`},
		{name: "invalid transfer source UUID", method: http.MethodPost, path: "/v1/transfers", key: "key", body: `{"sourceWalletId":"bad","destinationWalletId":"` + testDestinationID + `","amount":"1.00","currency":"USD"}`},
		{name: "invalid history cursor", method: http.MethodGet, path: "/v1/wallets/" + testWalletID + "/transactions?cursor=bad!"},
		{name: "history limit zero", method: http.MethodGet, path: "/v1/wallets/" + testWalletID + "/transactions?limit=0"},
		{name: "invalid history type", method: http.MethodGet, path: "/v1/wallets/" + testWalletID + "/transactions?type=refund"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := performRequest(testHandler(), test.method, test.path, test.body, "application/json", test.key)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
			}
			assertJSONContains(t, response, `"code":"INVALID_REQUEST"`)
		})
	}
}

func TestHealthAndMiddlewareBehavior(t *testing.T) {
	t.Parallel()
	t.Run("liveness and security headers", func(t *testing.T) {
		service := defaultStubService()
		response := performRequest(testAPIHandler(service), http.MethodGet, "/health/live", "", "", "")
		if response.Code != http.StatusOK || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected health response: status=%d headers=%v", response.Code, response.Header())
		}
	})
	t.Run("readiness failure", func(t *testing.T) {
		service := defaultStubService()
		service.ping = func(context.Context) error { return errors.New("down") }
		response := performRequest(testAPIHandler(service), http.MethodGet, "/health/ready", "", "", "")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", response.Code)
		}
		assertJSONContains(t, response, `"code":"NOT_READY"`)
	})
	t.Run("request ID is preserved", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("X-Request-ID", "caller-request-id")
		response := httptest.NewRecorder()
		testAPIHandler(defaultStubService()).ServeHTTP(response, request)
		if response.Header().Get("X-Request-ID") != "caller-request-id" {
			t.Fatalf("request ID was not preserved")
		}
	})
	t.Run("panic is recovered", func(t *testing.T) {
		service := defaultStubService()
		service.getWallet = func(context.Context, string) (domain.Wallet, error) { panic("boom") }
		response := performRequest(testAPIHandler(service), http.MethodGet, "/v1/wallets/"+testWalletID, "", "", "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", response.Code, response.Body.String())
		}
	})
	t.Run("unknown route", func(t *testing.T) {
		response := performRequest(testAPIHandler(defaultStubService()), http.MethodGet, "/unknown", "", "", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", response.Code)
		}
		assertJSONContains(t, response, `"code":"ROUTE_NOT_FOUND"`)
	})
	t.Run("request timeout is propagated", func(t *testing.T) {
		service := defaultStubService()
		service.getWallet = func(ctx context.Context, _ string) (domain.Wallet, error) {
			<-ctx.Done()
			return domain.Wallet{}, ctx.Err()
		}
		ids := identity.UUIDGenerator{}
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		handler := NewHandler(service, logger).Routes(5*time.Millisecond, ids)
		response := performRequest(handler, http.MethodGet, "/v1/wallets/"+testWalletID, "", "", "")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d: %s", response.Code, response.Body.String())
		}
		assertJSONContains(t, response, `"code":"REQUEST_CANCELLED"`)
	})
}

func testHandler() http.Handler {
	return testAPIHandler(defaultStubService())
}

func testAPIHandler(service walletService) http.Handler {
	ids := identity.UUIDGenerator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(service, logger).Routes(time.Second, ids)
}

func defaultStubService() *stubService {
	return &stubService{
		createWallet: func(context.Context, application.CreateWalletCommand) (domain.Wallet, bool, error) {
			return domain.Wallet{}, false, nil
		},
		getWallet: func(context.Context, string) (domain.Wallet, error) { return domain.Wallet{}, nil },
		deposit: func(context.Context, application.MoneyCommand) (domain.OperationResult, error) {
			return domain.OperationResult{}, nil
		},
		withdraw: func(context.Context, application.MoneyCommand) (domain.OperationResult, error) {
			return domain.OperationResult{}, nil
		},
		transfer: func(context.Context, application.TransferCommand) (domain.OperationResult, error) {
			return domain.OperationResult{}, nil
		},
		getTransaction: func(context.Context, string) (domain.OperationResult, error) { return domain.OperationResult{}, nil },
		listHistory: func(context.Context, string, int64, int, *domain.TransactionType) (domain.HistoryPage, error) {
			return domain.HistoryPage{}, nil
		},
		ping: func(context.Context) error { return nil },
	}
}

func performJSONRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	return performRequest(handler, method, path, body, "application/json", key)
}

func performRequest(handler http.Handler, method, path, body, contentType, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	request.Header.Set("X-Client-ID", "client-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertJSONContains(t *testing.T, response *httptest.ResponseRecorder, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(response.Body.String(), value) {
			t.Fatalf("response body %s does not contain %s", response.Body.String(), value)
		}
	}
}

func testWallet() domain.Wallet {
	return domain.Wallet{ID: testWalletID, Currency: "USD", BalanceMinor: 1000, Status: domain.WalletActive, Version: 2, CreatedAt: testTime(), UpdatedAt: testTime()}
}

func testOperation(kind domain.TransactionType) domain.OperationResult {
	source, destination := testWalletID, testDestinationID
	transaction := domain.FinancialTransaction{ID: testTransactionID, Type: kind, AmountMinor: 1234, Currency: "USD", CreatedAt: testTime()}
	direction := domain.EntryCredit
	entryWalletID := testWalletID
	switch kind {
	case domain.TransactionDeposit:
		transaction.DestinationWalletID = &destination
	case domain.TransactionWithdrawal:
		transaction.SourceWalletID = &source
		direction = domain.EntryDebit
	case domain.TransactionTransfer:
		transaction.SourceWalletID = &source
		transaction.DestinationWalletID = &destination
		direction = domain.EntryDebit
	}
	return domain.OperationResult{Transaction: transaction, Entries: []domain.LedgerEntry{{
		WalletID: entryWalletID, Direction: direction, BalanceAfterMinor: 1000,
	}}}
}

func testTime() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

var _ walletService = (*stubService)(nil)
