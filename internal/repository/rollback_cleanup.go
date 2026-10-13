package repository

import (
	"database/sql"
	"errors"
	"log/slog"
)

// LogRollbackError accepts a prior commit and reports unexpected cleanup errors.
func LogRollbackError(err error) {
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Warn("rollback transaction", "err", err)
	}
}
