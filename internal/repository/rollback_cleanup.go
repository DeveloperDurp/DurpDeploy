package repository

import (
	"database/sql"
	"errors"
	"log/slog"
)

// RollbackOnExit releases unfinished transactions and accepts a prior commit.
func RollbackOnExit(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Warn("rollback transaction", "err", err)
	}
}
