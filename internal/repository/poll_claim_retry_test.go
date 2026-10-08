package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
)

func TestPollClaimRetryBoundsAndClassifiesSQLServerErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		attempts int
	}{
		{"deadlock", fmt.Errorf("claim: %w", mssql.Error{Number: 1205}), 3},
		{"lock timeout", fmt.Errorf("claim: %w", mssql.Error{Number: 1222}), 1},
		{"unique constraint", fmt.Errorf("claim: %w", mssql.Error{Number: 2601}), 1},
		{"text alone", errors.New("mssql deadlock 1205"), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			value, err := withPollClaimRetryValue(
				t.Context(),
				func() (int, error) {
					attempts++
					return 99, test.err
				},
			)
			if attempts != test.attempts || !errors.Is(err, test.err) ||
				value != 0 {
				t.Fatalf("attempts=%d value=%d error=%v", attempts, value, err)
			}
		})
	}
}

func TestPollClaimRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	err := withPollClaimRetry(ctx, func() error {
		attempts++
		cancel()
		return mssql.Error{Number: 1205}
	})
	if attempts != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("attempts=%d error=%v", attempts, err)
	}
}
