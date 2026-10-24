package api_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/testutil"
	"durpdeploy/internal/testutil/sqlitefixture"
)

func apiFixtureDSN(path string) string {
	return "file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

func newAPITestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	directory, err := os.MkdirTemp(apiFixtureRoot, "database-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove API fixture: %v", err)
		}
	})
	return sqlitefixture.Open(t, filepath.Join(directory, "test.db"),
		"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
}

func TestAPIFixtureDatabaseCloseDrainsConnections(t *testing.T) {
	conn := newAPITestDatabase(t)
	held, err := conn.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { testutil.Close(t, held) })
	closed := make(chan error, 1)
	go func() { closed <- sqlitefixture.Close(conn) }()
	require.Eventually(t, func() bool {
		return conn.PingContext(t.Context()) != nil
	}, time.Second, time.Millisecond)
	require.Never(t, func() bool {
		select {
		case err := <-closed:
			t.Logf(
				"database cleanup returned with an active connection: %v",
				err,
			)
			return true
		default:
			return false
		}
	}, 50*time.Millisecond, time.Millisecond)
	_, err = held.ExecContext(t.Context(),
		"INSERT INTO projects(name) VALUES('finishing-telemetry')")
	require.NoError(t, err)
	require.NoError(t, held.Close())
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("database cleanup did not drain the released connection")
	}
}

func TestAPIFixtureDatabaseIsolationAndPersistence(t *testing.T) {
	first, second := newAPITestDatabase(t), newAPITestDatabase(t)
	paths := make([]string, 2)
	for i, conn := range []*sql.DB{first, second} {
		if err := conn.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&paths[i]); err != nil {
			t.Fatal(err)
		}
		var enabled int
		if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil ||
			enabled != 1 {
			t.Fatalf("foreign keys=%d, error=%v", enabled, err)
		}
	}
	if paths[0] == "" || paths[0] == paths[1] {
		t.Fatalf("fixtures share storage: %v", paths)
	}
	if _, err := first.Exec("INSERT INTO projects(name) VALUES('fixture-isolation')"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec("INSERT INTO steps(project_id, name, script_body) VALUES(-1, 'invalid', 'true')"); err == nil ||
		!strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatal("fixture accepted a missing project reference")
	}
	var count int
	if err := second.QueryRow("SELECT count(*) FROM projects").Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("another fixture saw writes: count=%d, error=%v", count, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := migrate.Run(apiFixtureDSN(paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := reopened.QueryRow("SELECT count(*) FROM projects").Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("fixture did not persist: count=%d, error=%v", count, err)
	}
}

func TestAPIFixtureSchemaMatchesFreshMigrations(t *testing.T) {
	cloned := newAPITestDatabase(t)
	fresh, err := migrate.Run(
		apiFixtureDSN(filepath.Join(t.TempDir(), "fresh.db")),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, query := range []string{
		"SELECT type, name, tbl_name, coalesce(sql, '') FROM sqlite_master ORDER BY type, name",
		"SELECT version_id, is_applied FROM goose_db_version ORDER BY id",
	} {
		if got, want := fixtureRows(t, cloned, query), fixtureRows(t, fresh, query); got != want {
			t.Fatalf(
				"fixture differs from migrations: %s\ngot %s\nwant %s",
				query,
				got,
				want,
			)
		}
	}
}

func fixtureRows(t *testing.T, conn *sql.DB, query string) string {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result += fmt.Sprintln(values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
