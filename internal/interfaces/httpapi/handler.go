package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"walletservice/internal/application"
	"walletservice/internal/domain"
	"walletservice/internal/platform/identity"
)

const maxRequestBodyBytes = 1 << 20

var errUnsupportedMediaType = errors.New("Content-Type must be application/json")

type Handler struct {
	service walletService
	logger  *slog.Logger
}

type walletService interface {
	CreateWallet(ctx context.Context, command application.CreateWalletCommand) (domain.Wallet, bool, error)
	GetWallet(ctx context.Context, ownerID, walletID string) (domain.Wallet, error)
	Deposit(ctx context.Context, command application.MoneyCommand) (domain.OperationResult, error)
	Withdraw(ctx context.Context, command application.MoneyCommand) (domain.OperationResult, error)
	Transfer(ctx context.Context, command application.TransferCommand) (domain.OperationResult, error)
	GetTransaction(ctx context.Context, ownerID, transactionID string) (domain.OperationResult, error)
	ListHistory(ctx context.Context, ownerID, walletID string, cursor int64, limit int, transactionType *domain.TransactionType) (domain.HistoryPage, error)
	Ping(ctx context.Context) error
}

func NewHandler(service walletService, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func (h *Handler) Routes(requestTimeout time.Duration, ids identity.Generator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", h.liveness)
	mux.HandleFunc("GET /health/ready", h.readiness)
	mux.HandleFunc("POST /v1/wallets", h.createWallet)
	mux.HandleFunc("GET /v1/wallets/{walletID}", h.getWallet)
	mux.HandleFunc("POST /v1/wallets/{walletID}/deposits", h.deposit)
	mux.HandleFunc("POST /v1/wallets/{walletID}/withdrawals", h.withdraw)
	mux.HandleFunc("GET /v1/wallets/{walletID}/transactions", h.listHistory)
	mux.HandleFunc("GET /v1/transactions/{transactionID}", h.getTransaction)
	mux.HandleFunc("POST /v1/transfers", h.transfer)
	mux.HandleFunc("/health/live", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("/health/ready", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("/v1/wallets", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/v1/wallets/{walletID}", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("/v1/wallets/{walletID}/deposits", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/v1/wallets/{walletID}/withdrawals", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/v1/wallets/{walletID}/transactions", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("/v1/transactions/{transactionID}", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("/v1/transfers", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/", notFound)
	return middlewareChain(mux, h.logger, requestTimeout, ids)
}

func (h *Handler) createWallet(writer http.ResponseWriter, request *http.Request) {
	var body createWalletRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		writeDecodeProblem(writer, request, err)
		return
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	idempotency, err := idempotencyFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	wallet, replayed, err := h.service.CreateWallet(request.Context(), application.CreateWalletCommand{
		OwnerID: ownerID, Currency: body.Currency, Idempotency: idempotency,
	})
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writer.Header().Set("Location", "/v1/wallets/"+wallet.ID)
	writeJSON(writer, status, presentWallet(wallet))
}

func (h *Handler) getWallet(writer http.ResponseWriter, request *http.Request) {
	walletID := request.PathValue("walletID")
	if !identity.IsUUID(walletID) {
		writeProblem(writer, validationProblem(request, "walletId must be a valid UUID"))
		return
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	wallet, err := h.service.GetWallet(request.Context(), ownerID, walletID)
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, presentWallet(wallet))
}

func (h *Handler) deposit(writer http.ResponseWriter, request *http.Request) {
	h.changeBalance(writer, request, h.service.Deposit)
}

func (h *Handler) withdraw(writer http.ResponseWriter, request *http.Request) {
	h.changeBalance(writer, request, h.service.Withdraw)
}

func (h *Handler) changeBalance(writer http.ResponseWriter, request *http.Request, operation func(requestContext context.Context, command application.MoneyCommand) (domain.OperationResult, error)) {
	walletID := request.PathValue("walletID")
	if !identity.IsUUID(walletID) {
		writeProblem(writer, validationProblem(request, "walletId must be a valid UUID"))
		return
	}
	var body moneyRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		writeDecodeProblem(writer, request, err)
		return
	}
	money, err := domain.ParseMoney(body.Amount, body.Currency)
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	idempotency, err := idempotencyFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	result, err := operation(request.Context(), application.MoneyCommand{
		OwnerID: ownerID, WalletID: walletID, Money: money, Idempotency: idempotency,
	})
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writer.Header().Set("Location", "/v1/transactions/"+result.Transaction.ID)
	writeJSON(writer, status, presentOperation(result))
}

func (h *Handler) transfer(writer http.ResponseWriter, request *http.Request) {
	var body transferRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		writeDecodeProblem(writer, request, err)
		return
	}
	if !identity.IsUUID(body.SourceWalletID) || !identity.IsUUID(body.DestinationWalletID) {
		writeProblem(writer, validationProblem(request, "sourceWalletId and destinationWalletId must be valid UUIDs"))
		return
	}
	money, err := domain.ParseMoney(body.Amount, body.Currency)
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	idempotency, err := idempotencyFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	result, err := h.service.Transfer(request.Context(), application.TransferCommand{
		OwnerID: ownerID, SourceWalletID: body.SourceWalletID, DestinationWalletID: body.DestinationWalletID,
		Money: money, Idempotency: idempotency,
	})
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writer.Header().Set("Location", "/v1/transactions/"+result.Transaction.ID)
	writeJSON(writer, status, presentOperation(result))
}

func (h *Handler) listHistory(writer http.ResponseWriter, request *http.Request) {
	walletID := request.PathValue("walletID")
	if !identity.IsUUID(walletID) {
		writeProblem(writer, validationProblem(request, "walletId must be a valid UUID"))
		return
	}
	cursor, err := decodeCursor(request.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	limit := 50
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		limit, err = strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 100 {
			writeProblem(writer, validationProblem(request, "limit must be between 1 and 100"))
			return
		}
	}
	var typeFilter *domain.TransactionType
	if rawType := strings.ToUpper(request.URL.Query().Get("type")); rawType != "" {
		value := domain.TransactionType(rawType)
		if value != domain.TransactionDeposit && value != domain.TransactionWithdrawal && value != domain.TransactionTransfer {
			writeProblem(writer, validationProblem(request, "type must be DEPOSIT, WITHDRAWAL, or TRANSFER"))
			return
		}
		typeFilter = &value
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	page, err := h.service.ListHistory(request.Context(), ownerID, walletID, cursor, limit, typeFilter)
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	items := make([]historyItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, historyItemResponse{
			ID: item.TransactionID, Type: string(item.Type), Direction: string(item.Direction),
			Amount: domain.FormatMinor(item.AmountMinor, item.Currency), Currency: item.Currency,
			BalanceAfter:         domain.FormatMinor(item.BalanceAfterMinor, item.Currency),
			CounterpartyWalletID: item.CounterpartyWalletID, CreatedAt: item.CreatedAt,
		})
	}
	writeJSON(writer, http.StatusOK, historyResponse{Items: items, NextCursor: encodeCursor(page.NextCursor)})
}

func (h *Handler) getTransaction(writer http.ResponseWriter, request *http.Request) {
	transactionID := request.PathValue("transactionID")
	if !identity.IsUUID(transactionID) {
		writeProblem(writer, validationProblem(request, "transactionId must be a valid UUID"))
		return
	}
	ownerID, err := ownerIDFromRequest(request)
	if err != nil {
		writeProblem(writer, validationProblem(request, err.Error()))
		return
	}
	result, err := h.service.GetTransaction(request.Context(), ownerID, transactionID)
	if err != nil {
		h.writeServiceError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, presentOperation(result))
}

func (h *Handler) liveness(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) readiness(writer http.ResponseWriter, request *http.Request) {
	if err := h.service.Ping(request.Context()); err != nil {
		writeProblem(writer, problem{
			Type: "https://wallet.example/problems/NOT_READY", Title: "Service unavailable",
			Status: http.StatusServiceUnavailable, Code: "NOT_READY",
			Detail: "The database is unavailable.", Instance: request.URL.Path,
			RequestID: requestIDFromContext(request.Context()),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func methodNotAllowed(allowedMethod string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Allow", allowedMethod)
		writeProblem(writer, problem{
			Type:  "https://wallet.example/problems/METHOD_NOT_ALLOWED",
			Title: "Method not allowed", Status: http.StatusMethodNotAllowed,
			Code: "METHOD_NOT_ALLOWED", Detail: "This resource does not support the requested HTTP method.",
			Instance: request.URL.Path, RequestID: requestIDFromContext(request.Context()),
		})
	}
}

func notFound(writer http.ResponseWriter, request *http.Request) {
	writeProblem(writer, problem{
		Type:  "https://wallet.example/problems/ROUTE_NOT_FOUND",
		Title: "Route not found", Status: http.StatusNotFound,
		Code: "ROUTE_NOT_FOUND", Detail: "The requested route does not exist.",
		Instance: request.URL.Path, RequestID: requestIDFromContext(request.Context()),
	})
}

func (h *Handler) writeServiceError(writer http.ResponseWriter, request *http.Request, err error) {
	problem := problemForError(request, err)
	if problem.Status >= 500 {
		h.logger.ErrorContext(request.Context(), "request failed", "error", err, "request_id", problem.RequestID)
	}
	writeProblem(writer, problem)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	contentType := request.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return errUnsupportedMediaType
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var syntaxError *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("malformed JSON at position %d", syntaxError.Offset)
		case errors.As(err, &typeError):
			return fmt.Errorf("field %q has an invalid value", typeError.Field)
		case errors.Is(err, io.EOF):
			return errors.New("request body is required")
		case errors.As(err, new(*http.MaxBytesError)):
			return err
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return errors.New(err.Error())
		default:
			return errors.New("request body is invalid")
		}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeDecodeProblem(writer http.ResponseWriter, request *http.Request, err error) {
	value := validationProblem(request, err.Error())
	var maxBytesError *http.MaxBytesError
	switch {
	case errors.Is(err, errUnsupportedMediaType):
		value.Type = "https://wallet.example/problems/UNSUPPORTED_MEDIA_TYPE"
		value.Title = "Unsupported media type"
		value.Status = http.StatusUnsupportedMediaType
		value.Code = "UNSUPPORTED_MEDIA_TYPE"
	case errors.As(err, &maxBytesError):
		value.Type = "https://wallet.example/problems/REQUEST_TOO_LARGE"
		value.Title = "Request too large"
		value.Status = http.StatusRequestEntityTooLarge
		value.Code = "REQUEST_TOO_LARGE"
		value.Detail = "The request body must not exceed 1 MiB."
	}
	writeProblem(writer, value)
}

func idempotencyFromRequest(request *http.Request) (application.Idempotency, error) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		return application.Idempotency{}, errors.New("Idempotency-Key header is required and must not exceed 128 characters")
	}
	return application.Idempotency{Key: key}, nil
}

func ownerIDFromRequest(request *http.Request) (string, error) {
	ownerID := strings.ToLower(strings.TrimSpace(request.Header.Get("X-User-ID")))
	if !identity.IsUUID(ownerID) {
		return "", errors.New("X-User-ID header is required and must be a valid UUID")
	}
	return ownerID, nil
}

func writeProblem(writer http.ResponseWriter, value problem) {
	writeJSONContentType(writer, value.Status, "application/problem+json", value)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writeJSONContentType(writer, status, "application/json", value)
}

func writeJSONContentType(writer http.ResponseWriter, status int, contentType string, value any) {
	writer.Header().Set("Content-Type", contentType)
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
