package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"durpdeploy/internal/dbconfig"
	"durpdeploy/internal/mssqldriver"
	_ "durpdeploy/internal/pgdriver"
	"durpdeploy/migrations"
)

// Run opens the database and applies all pending migrations. A sqlserver://
// DSN uses internal/mssqldriver with the embedded native SQL Server migrations.
// PostgreSQL DSNs use internal/pgdriver, and all other DSNs are SQLite paths.
func Run(dsn string) (*sql.DB, error) {
	security, err := dbconfig.InspectConnectionSecurity(dsn)
	if err != nil {
		return nil, err
	}
	if security.Warning != "" {
		slog.Warn("database transport security warning",
			"backend", security.Backend,
			"policy", security.Policy,
			"warning", security.Warning,
		)
	}
	if isSQLServer(dsn) {
		config, err := migrationConfig(dsn)
		if err != nil {
			return nil, err
		}
		return runMigrations(
			config.dsn,
			config.driverName,
			config.gooseDialect,
			config.migrationFS,
		)
	}
	if isPostgres(dsn) {
		return runMigrations(dsn, "pgx-qmark", "postgres", migrations.FS)
	}
	return runMigrations(
		sqliteDSNWithDefaults(dsn), "sqlite", "sqlite3", migrations.FS,
	)
}

// sqliteDSNWithDefaults gives bare paths the same connection settings as
// defaultDSN. A query string represents explicit operator configuration.
func sqliteDSNWithDefaults(dsn string) string {
	if strings.Contains(dsn, "?") {
		return dsn
	}
	return dsn +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)"
}

type migrationSettings struct {
	dsn          string
	driverName   string
	gooseDialect string
	migrationFS  fs.FS
}

func migrationConfig(dsn string) (migrationSettings, error) {
	mssqlFS, err := fs.Sub(migrations.MSSQLFS, "mssql")
	if err != nil {
		return migrationSettings{}, fmt.Errorf("mssql migrations: %w", err)
	}
	return migrationSettings{
		dsn:          dsnWithSecureDefaults(dsn),
		driverName:   mssqldriver.DriverName,
		gooseDialect: "mssql",
		migrationFS:  mssqlFS,
	}, nil
}

func isSQLServer(dsn string) bool {
	return dbconfig.IsSQLServer(dsn)
}

func dsnWithSecureDefaults(dsn string) string {
	return dbconfig.SQLServerDSN(dsn)
}

func isPostgres(dsn string) bool {
	return dbconfig.IsPostgres(dsn)
}

func runMigrations(
	dsn, driverName, gooseDialect string, migrationFS fs.FS,
) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, errors.Join(fmt.Errorf("ping db: %w", err), db.Close())
	}

	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect(gooseDialect); err != nil {
		return nil, errors.Join(
			fmt.Errorf("set Goose dialect: %w", err), db.Close(),
		)
	}
	if err := goose.Up(db, "."); err != nil {
		return nil, errors.Join(
			fmt.Errorf("run migrations: %w", err), db.Close(),
		)
	}

	return db, nil
}
