package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

var errStartupOperation = errors.New("startup operation failed")
var errStartupClose = errors.New("startup close failed")

type startupFailureDriver struct{}
type startupFailureConn struct{ failPing bool }

func (startupFailureDriver) Open(name string) (driver.Conn, error) {
	return startupFailureConn{failPing: name == "ping"}, nil
}

func (startupFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errStartupOperation
}

func (startupFailureConn) Begin() (driver.Tx, error) {
	return nil, errStartupOperation
}

func (startupFailureConn) Close() error { return errStartupClose }

func (c startupFailureConn) Ping(context.Context) error {
	if c.failPing {
		return errStartupOperation
	}
	return nil
}

func init() {
	sql.Register("migration-startup-close-failure", startupFailureDriver{})
}

func TestRunMigrationsPreservesStartupAndCloseErrors(t *testing.T) {
	t.Cleanup(func() {
		goose.SetBaseFS(migrations.FS)
		if err := goose.SetDialect("sqlite3"); err != nil {
			t.Error(err)
		}
	})
	files := fstest.MapFS{
		"00001_test.sql": {
			Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;"),
		},
	}
	for _, tc := range []struct {
		stage   string
		dsn     string
		dialect string
	}{
		{"ping db", "ping", "sqlite3"},
		{"set Goose dialect", "dialect", "unknown"},
		{"run migrations", "migrations", "sqlite3"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			conn, err := runMigrations(
				tc.dsn, "migration-startup-close-failure", tc.dialect, files,
			)
			if conn != nil || err == nil ||
				!strings.Contains(err.Error(), tc.stage) ||
				!errors.Is(err, errStartupClose) {
				t.Fatalf("database=%v error=%v", conn, err)
			}
			if tc.dsn != "dialect" && !errors.Is(err, errStartupOperation) {
				t.Fatalf("startup error was lost: %v", err)
			}
		})
	}
}
