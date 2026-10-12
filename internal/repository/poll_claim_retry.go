package repository

import (
	"context"
	"errors"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

func withPollClaimRetry(ctx context.Context, operation func() error) error {
	_, err := withPollClaimRetryValue(ctx, func() (struct{}, error) {
		return struct{}{}, operation()
	})
	return err
}

// SQL Server rolls back a deadlock victim before returning error 1205. Retry
// the complete claim, including candidate selection and payload preparation.
func withPollClaimRetryValue[T any](
	ctx context.Context, operation func() (T, error),
) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result, err := withSQLiteBusyRetryValue(ctx, operation)
		if err == nil {
			return result, nil
		}
		var deadlock mssql.Error
		if attempt >= 2 || !errors.As(err, &deadlock) ||
			deadlock.Number != 1205 {
			return zero, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
