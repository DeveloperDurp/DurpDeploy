package artifact

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSpoolCleanupPreservesSuccessfulOperation(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if err := SpoolCleanupError(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful cleanup logged a warning: %s", &logs)
	}
	closeErr := errors.New("spool close failed")
	if err := SpoolCleanupError(t.Context(), nil, closeErr); err != nil {
		t.Fatalf("cleanup reversed the successful operation: %v", err)
	}
	if !strings.Contains(logs.String(), "close temporary spool") ||
		!strings.Contains(logs.String(), closeErr.Error()) {
		t.Fatalf("cleanup error was not reported: %s", &logs)
	}
}

func TestSpoolCleanupPreservesFailedOperation(t *testing.T) {
	operationErr := errors.New("publish gate failed")
	closeErr := errors.New("spool close failed")
	if err := SpoolCleanupError(t.Context(), operationErr, nil); !errors.Is(
		err,
		operationErr,
	) {
		t.Fatalf("operation error was lost: %v", err)
	}
	err := SpoolCleanupError(t.Context(), operationErr, closeErr)
	if !errors.Is(err, operationErr) || !errors.Is(err, closeErr) {
		t.Fatalf("operation or cleanup error was lost: %v", err)
	}
}
