package migrate

import (
	"database/sql"
	"testing"

	"durpdeploy/migrations"
	"github.com/pressly/goose/v3"
)

func TestLocalAdministrationMigrationPreservesUsers(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "SQLite dialect")
	requireNoError(t, goose.UpTo(conn, ".", 66), "existing schema")
	_, err = conn.Exec(
		"INSERT INTO users (email,password_hash,name,role) VALUES ('migration@example.test','retained','Retained administrator','admin')",
	)
	requireNoError(t, err, "existing administrator")
	for range 2 {
		requireNoError(
			t,
			goose.UpTo(conn, ".", 67),
			"local administration schema",
		)
		var version int
		requireNoError(
			t,
			conn.QueryRow("SELECT version FROM local_administration_lock WHERE id=1").
				Scan(&version),
			"seeded coordination row",
		)
		if version != 0 {
			t.Fatal("coordination seed changed")
		}
		for _, query := range []string{
			"INSERT INTO local_administration_lock (id,version) VALUES (2,0)",
			"UPDATE local_administration_lock SET version=2 WHERE id=1",
		} {
			if _, err := conn.Exec(query); err == nil {
				t.Fatal("coordination constraints did not reject invalid state")
			}
		}
		requireNoError(
			t,
			goose.DownTo(conn, ".", 66),
			"rollback local administration schema",
		)
		var role, hash string
		requireNoError(
			t,
			conn.QueryRow("SELECT role,password_hash FROM users WHERE email='migration@example.test'").
				Scan(&role, &hash),
			"preserved administrator",
		)
		if role != "admin" || hash != "retained" {
			t.Fatal("coordination migration changed the administrator")
		}
	}
}
