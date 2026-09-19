package migrate

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

func TestSQLite_OIDCMigrationRollsBackAndReapplies(t *testing.T) {
	// Given: SQLite migrated through the OIDC schema version.
	ctx := context.Background()
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate SQLite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "SQLite dialect")
	requireNoError(t, goose.UpTo(conn, ".", 24), "OIDC schema")

	// When: the OIDC migration is rolled back and reapplied.
	if err := goose.DownTo(conn, ".", 23); err != nil {
		t.Fatalf("roll back OIDC migration: %v", err)
	}
	if err := goose.UpTo(conn, ".", 24); err != nil {
		t.Fatalf("reapply OIDC migration: %v", err)
	}

	// Then: both OIDC tables are present after the reversible migration cycle.
	for _, table := range []string{"oidc_identities", "oidc_transactions"} {
		var name string
		err := conn.QueryRowContext(ctx, `
			SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("find re-applied OIDC table %s: %v", table, err)
		}
	}
}
