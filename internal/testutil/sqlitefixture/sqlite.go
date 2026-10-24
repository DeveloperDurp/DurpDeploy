// Package sqlitefixture provides isolated copies of a migrated test database.
package sqlitefixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/migrate"
)

var schema = sync.OnceValues(func() (data []byte, err error) {
	directory, err := os.MkdirTemp(
		os.Getenv("DURPDEPLOY_GO_TEST_DIRECTORY"), "sqlite-template-",
	)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	path := filepath.Join(directory, "template.db")
	conn, err := migrate.Run("file:" + path + "?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// Close before copying so each fixture receives a complete, empty schema.
	if err := Close(conn); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.ReadFile("template.db")
})

// Open copies the blank schema to a new file owned by the caller's test.
// The caller owns the containing directory. Cleanup drains connections first.
func Open(t *testing.T, path, pragmas string) *sql.DB {
	t.Helper()
	data, err := schema()
	if err != nil {
		t.Fatalf("migrate fixture template: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the real startup path and each harness's connection pragmas.
	conn, err := migrate.Run("file:" + path + "?" + pragmas)
	if err != nil {
		t.Fatalf("open migrated fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(conn); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	return conn
}

// Close waits for active writes before the caller removes the database files.
func Close(conn *sql.DB) error {
	if err := conn.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for conn.Stats().OpenConnections != 0 {
		select {
		case <-ctx.Done():
			return fmt.Errorf("drain fixture connections: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}
