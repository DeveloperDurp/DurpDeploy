//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

type restoreBackend string

const (
	restoreSQLiteBackend   restoreBackend = "sqlite"
	restorePostgresBackend restoreBackend = "postgres"
)

func selectedRestoreBackend(t *testing.T) restoreBackend {
	t.Helper()
	value := os.Getenv("DURPDEPLOY_RESTORE_BACKEND")
	if value == "" {
		return restoreSQLiteBackend
	}
	switch backend := restoreBackend(value); backend {
	case restoreSQLiteBackend, restorePostgresBackend:
		return backend
	default:
		t.Fatal("set DURPDEPLOY_RESTORE_BACKEND to sqlite or postgres")
		return ""
	}
}

// All three native recovery implementations use the same application flow.
type restoreBackup interface {
	awaitFresh(*testing.T, int64)
	recover(*testing.T)
}

func (f *restoreFixture) backup(t *testing.T) restoreBackup {
	t.Helper()
	switch f.backend {
	case restoreSQLiteBackend:
		missing := f.litestreamContainer(t, "restore", "-o",
			"/recovery/backup/missing.db", "file:///recovery/backup/absent")
		if restoreContainerExit(t, missing) == 0 {
			t.Fatal("restore accepted an absent replica")
		}
		return f.replicate(t)
	case restorePostgresBackend:
		return &restorePostgresBackup{fixture: f}
	default:
		t.Fatal("unsupported recovery backend")
		return nil
	}
}

func (f *restoreFixture) assertNativeRestored(t *testing.T, id int64) {
	t.Helper()
	conn := restoreOpen(t, "pgx-qmark", f.dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, table := range []string{"goose_db_version", "users", "releases",
		"release_variables", "deployment_logs", "scheduled_deployments"} {
		var count int64
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).
			Scan(&count); err != nil || count == 0 {
			t.Fatalf("restore lost schema or seeded rows in %s: %v", table, err)
		}
	}
	var count int
	if err := conn.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM projects WHERE id = ? AND name = 'backup-watermark'",
		id,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restore lost the final API write: %v", err)
	}
	if _, err := db.New(conn).ListVariablesByProjectPaginated(ctx,
		db.ListVariablesByProjectPaginatedParams{ProjectID: f.api.project.ID,
			PageLimit: 20}); err != nil {
		t.Fatalf("read restored variables as application role: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}
