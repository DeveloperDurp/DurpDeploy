package repository

import (
	"database/sql"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestRepositoryDatabaseIsolation(t *testing.T) {
	for _, engine := range []string{"PostgreSQL", "SQLServer"} {
		t.Run(engine, func(t *testing.T) {
			checkRepositoryDatabaseIsolation(t, engine)
		})
	}
}

func checkRepositoryDatabaseIsolation(t *testing.T, engine string) {
	t.Helper()
	first := RepositoryDatabaseDSN(t, engine)
	connection := openFixtureProbe(t, engine, first)
	if _, err := connection.ExecContext(t.Context(),
		"CREATE TABLE fixture_probe (value INT)"); err != nil {
		t.Fatal(err)
	}
	var second string
	if !t.Run("independent database", func(t *testing.T) {
		second = checkIndependentFixture(t, engine, first)
	}) {
		return
	}
	// The child context is canceled, but its database must be gone.
	dsn, err := url.Parse(second)
	if err != nil {
		t.Fatal(err)
	}
	server := &repositoryPostgres
	database := strings.TrimPrefix(dsn.Path, "/")
	query := "SELECT COUNT(*) FROM pg_database WHERE datname=$1"
	if engine == "SQLServer" {
		server, database = &repositorySQLServer, dsn.Query().
			Get("database")
		query = "SELECT COUNT(*) FROM sys.databases WHERE name=@p1"
	}
	var count int
	if err := server.admin.QueryRowContext(t.Context(), query,
		database).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("fixture database survived cleanup")
	}
	if _, err := connection.ExecContext(t.Context(),
		"INSERT INTO fixture_probe VALUES (1)"); err != nil {
		t.Fatalf("cleanup affected the neighboring fixture: %v", err)
	}
	t.Run("concurrent databases", func(t *testing.T) {
		for i := range 2 {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				dsn := RepositoryDatabaseDSN(t, engine)
				probe := openFixtureProbe(t, engine, dsn)
				if _, err := probe.ExecContext(t.Context(),
					"CREATE TABLE fixture_probe (value INT)"); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

func checkIndependentFixture(t *testing.T, engine, first string) string {
	t.Helper()
	second := RepositoryDatabaseDSN(t, engine)
	left, err := url.Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := url.Parse(second)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || left.Host != right.Host {
		t.Fatal("fixtures need separate databases on the same server")
	}
	other := openFixtureProbe(t, engine, second)
	var count int
	if err := other.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM information_schema.tables
WHERE table_name='fixture_probe'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("a fixture inherited another database's schema")
	}
	if _, err := other.ExecContext(t.Context(),
		"CREATE TABLE fixture_probe (value INT)"); err != nil {
		t.Fatal(err)
	}
	return second
}

func openFixtureProbe(t *testing.T, engine, dsn string) *sql.DB {
	t.Helper()
	driver := "pgx"
	if engine == "SQLServer" {
		driver = "sqlserver"
	}
	connection, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	return connection
}
