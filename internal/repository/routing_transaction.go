package repository

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"fmt"
	"strings"
	"time"
)

const routingTransactionAttempts = 20

func (r *Repository) WithRoutingTx(
	ctx context.Context,
	fn func(queries *db.Queries) error,
) error {
	var lastErr error
	for attempt := range routingTransactionAttempts {
		tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{
			Isolation: sql.LevelSerializable,
		})
		if err == nil {
			err = fn(r.Queries.WithTx(tx))
			if err == nil {
				err = tx.Commit()
				if err != nil {
					_ = tx.Rollback()
				}
			} else {
				_ = tx.Rollback()
			}
		}
		if err == nil || !isRetryableRoutingConflict(err) {
			return err
		}
		lastErr = err
		delay := time.Duration(attempt+1) * 5 * time.Millisecond
		select {
		case <-ctx.Done():
			return fmt.Errorf("routing transaction interrupted: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	return fmt.Errorf(
		"routing transaction contention after %d attempts: %w",
		routingTransactionAttempts,
		lastErr,
	)
}

func isRetryableRoutingConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "sqlstate 40001") ||
		strings.Contains(message, "deadlock victim") ||
		strings.Contains(message, "error 1205")
}
