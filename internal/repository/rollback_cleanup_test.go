package repository

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestLogRollbackErrorReportsUnexpectedErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	LogRollbackError(nil)
	LogRollbackError(fmt.Errorf("committed: %w", sql.ErrTxDone))
	if logs.Len() != 0 {
		t.Fatalf("completed transaction logged an error: %s", &logs)
	}
	LogRollbackError(errors.New("connection lost"))
	if !strings.Contains(logs.String(), "rollback transaction") ||
		!strings.Contains(logs.String(), "connection lost") {
		t.Fatalf("unexpected rollback error was not reported: %s", &logs)
	}
}
