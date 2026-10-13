package testutil

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Embed the test interface and capture expected diagnostics without failing it.
type cleanupTestRecorder struct {
	testing.TB
	messages []string
}

func (r *cleanupTestRecorder) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *cleanupTestRecorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	panic(r)
}

type cleanupTestCloser func() error

func (resource cleanupTestCloser) Close() error { return resource() }

type failedTestReader struct{ err error }

func (r failedTestReader) Read([]byte) (int, error) { return 0, r.err }

func TestCloseReportsUnexpectedCleanupFailure(t *testing.T) {
	recorder := &cleanupTestRecorder{TB: t}
	for _, err := range []error{nil, net.ErrClosed, sql.ErrConnDone} {
		Close(recorder, cleanupTestCloser(func() error { return err }))
	}
	if len(recorder.messages) != 0 {
		t.Fatalf("expected cleanup produced diagnostics: %v", recorder.messages)
	}
	Close(recorder, cleanupTestCloser(func() error {
		return errors.New("flush failed")
	}))
	if len(recorder.messages) != 1 ||
		recorder.messages[0] != "close test resource: flush failed" {
		t.Fatalf("unexpected cleanup was not reported: %v", recorder.messages)
	}
}

func TestReadFromStopsOnIncompleteResponse(t *testing.T) {
	recorder := &cleanupTestRecorder{TB: t}
	var body bytes.Buffer
	var stopped any
	func() {
		defer func() { stopped = recover() }()
		ReadFrom(recorder, &body, io.MultiReader(
			strings.NewReader("partial"),
			failedTestReader{err: errors.New("response interrupted")},
		))
	}()
	if stopped != recorder || len(recorder.messages) != 1 ||
		recorder.messages[0] != "read test response: response interrupted" {
		t.Fatalf("incomplete response did not stop: %v", recorder.messages)
	}
}

func TestRemoveReportsFailureAndAcceptsMissingFile(t *testing.T) {
	recorder := &cleanupTestRecorder{TB: t}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	Remove(recorder, filepath.Join(directory, "missing"))
	if len(recorder.messages) != 0 {
		t.Fatalf("missing file produced diagnostics: %v", recorder.messages)
	}
	Remove(recorder, directory)
	if len(recorder.messages) != 1 ||
		!strings.Contains(recorder.messages[0], "remove test resource:") {
		t.Fatalf("cleanup failure was not reported: %v", recorder.messages)
	}
}
