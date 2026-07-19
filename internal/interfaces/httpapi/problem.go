package httpapi

import (
	"context"
	"errors"
	"net/http"

	"walletservice/internal/domain"
)

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	RequestID string `json:"requestId,omitempty"`
}

func problemForError(request *http.Request, err error) problem {
	status, code, title, detail := http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "The request could not be completed."
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, code, title, detail = http.StatusServiceUnavailable, "REQUEST_CANCELLED", "Request could not be completed", "The request was cancelled or exceeded its deadline."
	case errors.Is(err, domain.ErrWalletNotFound), errors.Is(err, domain.ErrTransactionNotFound):
		status, code, title, detail = http.StatusNotFound, "RESOURCE_NOT_FOUND", "Resource not found", err.Error()
	case errors.Is(err, domain.ErrWalletAccessDenied):
		status, code, title, detail = http.StatusForbidden, "WALLET_ACCESS_DENIED", "Wallet access denied", err.Error()
	case errors.Is(err, domain.ErrOwnerHasWallet):
		status, code, title, detail = http.StatusConflict, "OWNER_ALREADY_HAS_WALLET", "Wallet already exists", err.Error()
	case errors.Is(err, domain.ErrInvalidOwner):
		status, code, title, detail = http.StatusBadRequest, "INVALID_OWNER", "Invalid owner", err.Error()
	case errors.Is(err, domain.ErrInvalidAmount):
		status, code, title, detail = http.StatusUnprocessableEntity, "INVALID_AMOUNT", "Invalid amount", err.Error()
	case errors.Is(err, domain.ErrInvalidCurrency):
		status, code, title, detail = http.StatusUnprocessableEntity, "INVALID_CURRENCY", "Invalid currency", err.Error()
	case errors.Is(err, domain.ErrCurrencyMismatch):
		status, code, title, detail = http.StatusUnprocessableEntity, "CURRENCY_MISMATCH", "Currency mismatch", err.Error()
	case errors.Is(err, domain.ErrInsufficientFunds):
		status, code, title, detail = http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", "Insufficient funds", err.Error()
	case errors.Is(err, domain.ErrSameWallet):
		status, code, title, detail = http.StatusUnprocessableEntity, "SAME_WALLET_TRANSFER", "Invalid transfer", err.Error()
	case errors.Is(err, domain.ErrWalletNotActive):
		status, code, title, detail = http.StatusConflict, "WALLET_NOT_ACTIVE", "Wallet is not active", err.Error()
	case errors.Is(err, domain.ErrIdempotencyConflict):
		status, code, title, detail = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency conflict", err.Error()
	case errors.Is(err, domain.ErrAmountOverflow):
		status, code, title, detail = http.StatusUnprocessableEntity, "AMOUNT_OUT_OF_RANGE", "Amount out of range", err.Error()
	}
	return problem{
		Type:  "https://wallet.example/problems/" + code,
		Title: title, Status: status, Code: code, Detail: detail,
		Instance: request.URL.Path, RequestID: requestIDFromContext(request.Context()),
	}
}

func validationProblem(request *http.Request, detail string) problem {
	return problem{
		Type:  "https://wallet.example/problems/INVALID_REQUEST",
		Title: "Invalid request", Status: http.StatusBadRequest,
		Code: "INVALID_REQUEST", Detail: detail, Instance: request.URL.Path,
		RequestID: requestIDFromContext(request.Context()),
	}
}
