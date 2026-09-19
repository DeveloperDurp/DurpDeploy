package repository

import (
	"context"
	"errors"
	"time"
)

const (
	sqliteBusyCode  = 5
	sqliteBusyTries = 5
)

type sqliteErrorCoder interface {
	Code() int
}

func IsSQLiteBusy(err error) bool {
	var sqliteErr sqliteErrorCoder
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteBusyCode
}

func withSQLiteBusyRetry(ctx context.Context, operation func() error) error {
	_, err := withSQLiteBusyRetryValue(ctx, func() (struct{}, error) {
		return struct{}{}, operation()
	})
	return err
}

func withSQLiteBusyRetryValue[T any](
	ctx context.Context,
	operation func() (T, error),
) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		result, err := operation()
		if err == nil {
			return result, nil
		}
		if attempt+1 >= sqliteBusyTries || !IsSQLiteBusy(err) {
			return zero, err
		}
		if !waitForSQLiteBusyRetry(ctx, attempt) {
			return zero, err
		}
	}
}

func waitForSQLiteBusyRetry(ctx context.Context, attempt int) bool {
	timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
