package migrate

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/internal/mssqldriver"
	"durpdeploy/migrations"
)

func TestIsSQLServer(t *testing.T) {
	for _, tc := range []struct {
		dsn  string
		want bool
	}{
		{"sqlserver://localhost", true},
		{" SQLSERVER://localhost", true},
		{"postgres://localhost/db", false},
		{"localhost.db", false},
	} {
		if got := isSQLServer(tc.dsn); got != tc.want {
			t.Errorf("isSQLServer(%q) = %v, want %v", tc.dsn, got, tc.want)
		}
	}
}

func TestIsPostgres(t *testing.T) {
	for _, tc := range []struct {
		dsn  string
		want bool
	}{
		{"postgres://localhost/db", true},
		{"postgresql://localhost/db", true},
		{"host=localhost user=postgres dbname=durpdeploy", true},
		{"user=postgres host=localhost", true},
		{"sqlserver://localhost", false},
		{"file:test.db", false},
	} {
		if got := isPostgres(tc.dsn); got != tc.want {
			t.Errorf("isPostgres(%q) = %v, want %v", tc.dsn, got, tc.want)
		}
	}
}

func TestMigrationRouteSelection_SQLServerPrefixOverridesPostgresHeuristic(
	t *testing.T,
) {
	dsn := "sqlserver://host:1433/example?host=localhost&user=sa"
	if got := isSQLServer(dsn); !got {
		t.Fatalf("isSQLServer(%q) = %v, want true", dsn, got)
	}

	if got := isPostgres(dsn); got == false {
		t.Fatalf("isPostgres(%q) = false, want true", dsn)
	}
}

func TestDSNWithSecureDefaults(t *testing.T) {
	got := dsnWithSecureDefaults("sqlserver://user:pass@host/db")
	if got != "sqlserver://user:pass@host/db?encrypt=true&trustservercertificate=false" {
		t.Fatalf("secure defaults = %q", got)
	}

	got = dsnWithSecureDefaults(
		"sqlserver://host/db?encrypt=false&trustservercertificate=true",
	)
	if got != "sqlserver://host/db?encrypt=false&trustservercertificate=true" {
		t.Fatalf("explicit overrides changed to %q", got)
	}

	got = dsnWithSecureDefaults(
		"sqlserver://host/db?Encrypt=false&TrustServerCertificate=true",
	)
	if got != "sqlserver://host/db?Encrypt=false&TrustServerCertificate=true" {
		t.Fatalf("case-insensitive overrides changed to %q", got)
	}

	got = dsnWithSecureDefaults("  SQLSERVER://host/db")
	if got != "sqlserver://host/db?encrypt=true&trustservercertificate=false" {
		t.Fatalf("trimmed secure defaults = %q", got)
	}

	got = dsnWithSecureDefaults(
		"sqlserver://host/db?Encrypt=false&TrustServerCertificate=true",
	)
	parsedGot, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parsing %q: %v", got, err)
	}
	if gotKey := parsedGot.Query().Get("Encrypt"); gotKey != "false" {
		t.Fatalf(
			"expected explicit Encrypt=false to be preserved, got %q",
			gotKey,
		)
	}
	if gotKey := parsedGot.Query().
		Get("TrustServerCertificate"); gotKey != "true" {
		t.Fatalf(
			"expected explicit TrustServerCertificate=true to be preserved, got %q",
			gotKey,
		)
	}
	if _, ok := parsedGot.Query()["encrypt"]; ok {
		t.Fatalf("added duplicate lowercase encrypt key: %q", got)
	}
	if _, ok := parsedGot.Query()["trustservercertificate"]; ok {
		t.Fatalf(
			"added duplicate lowercase trustservercertificate key: %q",
			got,
		)
	}
}

func TestMigrationConfig_SQLServerUsesWrapperAndNativeMigrations(t *testing.T) {
	// Given a SQL Server DSN.
	config, err := migrationConfig("sqlserver://host/database")
	if err != nil {
		t.Fatalf("migrationConfig: %v", err)
	}

	// When its startup configuration is selected.
	_, err = fs.ReadFile(config.migrationFS, "001_schema.sql")
	if err != nil {
		t.Fatalf("read native migration: %v", err)
	}

	// Then it uses the query-rewriting driver and SQL Server Goose dialect.
	if config.driverName != mssqldriver.DriverName {
		t.Errorf(
			"driver name = %q, want %q",
			config.driverName,
			mssqldriver.DriverName,
		)
	}
	if config.gooseDialect != "mssql" {
		t.Errorf("Goose dialect = %q, want mssql", config.gooseDialect)
	}
}

func TestRun_BareSQLitePathGetsConcurrencyAndIntegrityPragmas(t *testing.T) {
	// Given a production-style bare SQLite path.
	dsn := filepath.Join(t.TempDir(), "durpdeploy.db")

	// When the shared open seam opens two pooled connections.
	pool, err := Run(dsn)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	first, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer first.Close()
	second, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer second.Close()

	// Then each connection has the same settings as the default DSN.
	for name, conn := range map[string]*sql.Conn{"first": first, "second": second} {
		for _, tc := range []struct {
			pragma string
			want   string
		}{
			{"busy_timeout", "5000"},
			{"foreign_keys", "1"},
			{"journal_mode", "wal"},
		} {
			var got string
			if err := conn.QueryRowContext(
				ctx, "PRAGMA "+tc.pragma,
			).Scan(&got); err != nil {
				t.Fatalf("%s connection PRAGMA %s: %v", name, tc.pragma, err)
			}
			if got != tc.want {
				t.Errorf(
					"%s connection PRAGMA %s = %q, want %q",
					name, tc.pragma, got, tc.want,
				)
			}
		}
	}
}

func TestRun_BareSQLitePathPreservesExplicitBusyTimeoutZero(t *testing.T) {
	// Given an explicit busy_timeout(0), an operator's deliberate choice to
	// fail fast instead of waiting on locks.
	dsn := filepath.Join(t.TempDir(), "durpdeploy.db") +
		"?_pragma=busy_timeout(0)"

	// When the shared open seam runs migrations on it.
	dbConn, err := Run(dsn)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer dbConn.Close()

	// Then the explicit zero is preserved, not overwritten by the default.
	var got string
	if err := dbConn.QueryRow(
		"PRAGMA busy_timeout",
	).Scan(&got); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if got != "0" {
		t.Errorf("PRAGMA busy_timeout = %q, want 0", got)
	}
}

func TestRun_PreservesExistingGooseMigrationHistory(t *testing.T) {
	// Given an existing SQLite database migrated with Goose's default table.
	dsn := filepath.Join(t.TempDir(), "durpdeploy.db")
	first, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open first database: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("sqlite"); err != nil {
		t.Fatalf("set Goose dialect: %v", err)
	}
	if err := goose.Up(first, "."); err != nil {
		t.Fatalf("seed historical migration history: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	// When the application starts again against that database.
	second, err := Run(dsn)
	if err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	defer second.Close()
}
