package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"walletservice/internal/application"
	"walletservice/internal/platform/clock"
	"walletservice/internal/platform/identity"
)

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

func testHandler() http.Handler {
	ids := identity.UUIDGenerator{}
	service := application.NewService(nil, ids, clock.System{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(service, logger).Routes(time.Second, ids)
}
