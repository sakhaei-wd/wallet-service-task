package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"walletservice/internal/platform/identity"
)

type contextKey string

const requestIDKey contextKey = "request-id"

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(body)
	r.bytes += written
	return written, err
}

func middlewareChain(handler http.Handler, logger *slog.Logger, requestTimeout time.Duration, ids identity.Generator) http.Handler {
	handler = recoverer(handler, logger)
	handler = timeout(handler, requestTimeout)
	handler = accessLog(handler, logger)
	handler = requestID(handler, ids)
	handler = securityHeaders(handler)
	return handler
}

func requestID(next http.Handler, ids identity.Generator) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if value == "" || len(value) > 128 {
			value, _ = ids.New()
		}
		writer.Header().Set("X-Request-ID", value)
		ctx := context.WithValue(request.Context(), requestIDKey, value)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.InfoContext(request.Context(), "http request",
			"method", request.Method, "path", request.URL.Path, "status", status,
			"bytes", recorder.bytes, "duration_ms", time.Since(started).Milliseconds(),
			"request_id", requestIDFromContext(request.Context()))
	})
}

func recoverer(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "panic recovered", "error", recovered, "stack", string(debug.Stack()))
				writeProblem(writer, problemForError(request, errors.New("panic recovered")))
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func timeout(next http.Handler, duration time.Duration) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), duration)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}
