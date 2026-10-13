package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
)

type serverCleanupCloser func() error

func (resource serverCleanupCloser) Close() error { return resource() }

func TestCloseOnExitReportsUnexpectedErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, err := range []error{nil, fmt.Errorf("listener: %w", net.ErrClosed)} {
		closeOnExit(serverCleanupCloser(func() error { return err }))
	}
	if logs.Len() != 0 {
		t.Fatalf("expected shutdown logged an error: %s", &logs)
	}
	closeOnExit(serverCleanupCloser(func() error {
		return errors.New("flush failed")
	}))
	if !strings.Contains(logs.String(), "close server resource") ||
		!strings.Contains(logs.String(), "flush failed") {
		t.Fatalf("unexpected cleanup error was not reported: %s", &logs)
	}
}
