package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryableTransactionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "serialization failure",
			err:  fmt.Errorf("commit: %w", &pgconn.PgError{Code: "40001"}),
			want: true,
		},
		{
			name: "deadlock detected",
			err:  &pgconn.PgError{Code: "40P01"},
			want: true,
		},
		{
			name: "unique violation",
			err:  &pgconn.PgError{Code: "23505"},
		},
		{
			name: "non postgres error",
			err:  errors.New("connection lost"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableTransactionError(test.err); got != test.want {
				t.Fatalf("isRetryableTransactionError() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestTransactionRetryDelayFor(t *testing.T) {
	tests := []struct {
		retry int
		want  time.Duration
	}{
		{retry: 1, want: 10 * time.Millisecond},
		{retry: 2, want: 20 * time.Millisecond},
		{retry: 3, want: 40 * time.Millisecond},
		{retry: 10, want: transactionRetryMax},
	}

	for _, test := range tests {
		if got := transactionRetryDelayFor(test.retry); got != test.want {
			t.Errorf("transactionRetryDelayFor(%d) = %s, want %s", test.retry, got, test.want)
		}
	}
}
