package testutil

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"testing"
)

// Close checks cleanup without moving it out of the caller's defer scope.
func Close(t testing.TB, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil &&
		!errors.Is(err, net.ErrClosed) &&
		!errors.Is(err, sql.ErrConnDone) {
		t.Errorf("close test resource: %v", err)
	}
}

// Rollback accepts the completed transaction left by a successful commit.
func Rollback(t testing.TB, tx *sql.Tx) {
	t.Helper()
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("rollback test transaction: %v", err)
	}
}

// ReadFrom fails the test before it can inspect an incomplete response.
func ReadFrom(t testing.TB, target io.ReaderFrom, source io.Reader) {
	t.Helper()
	if _, err := target.ReadFrom(source); err != nil {
		t.Fatalf("read test response: %v", err)
	}
}

func Remove(t testing.TB, name string) {
	t.Helper()
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("remove test resource: %v", err)
	}
}
