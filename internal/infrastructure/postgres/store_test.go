package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, want: true},
		{name: "serialization failure", err: &pgconn.PgError{Code: "40001"}, want: true},
		{name: "wrapped transient error", err: fmt.Errorf("commit: %w", &pgconn.PgError{Code: "40001"}), want: true},
		{name: "constraint violation", err: &pgconn.PgError{Code: "23514"}},
		{name: "domain error", err: errors.New("insufficient funds")},
		{name: "nil", err: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryable(test.err); got != test.want {
				t.Fatalf("expected %v, got %v", test.want, got)
			}
		})
	}
}
