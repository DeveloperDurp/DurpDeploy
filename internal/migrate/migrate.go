package migrate

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	_ "durpdeploy/internal/pgdriver"
	"durpdeploy/migrations"
)

// Run opens the database and applies all pending migrations. The driver is
// detected from the DSN: a "postgres://" or "postgresql://" DSN talks to
// PostgreSQL (via internal/pgdriver, which rewrites the SQLite-flavored
// migrations/queries on the fly - see that package for why there's only one
// copy of the SQL); anything else is treated as a SQLite file path, the
// historical behavior.
func Run(dsn string) (*sql.DB, error) {
	if isPostgres(dsn) {
		return run(dsn, "pgx-qmark", "postgres")
	}
	return run(dsn, "sqlite", "sqlite3")
}

func isPostgres(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") ||
		strings.HasPrefix(dsn, "postgresql://") ||
		strings.Contains(dsn, "host=") ||
		strings.Contains(dsn, "user=")
}

func run(dsn, driverName, gooseDialect string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	goose.SetDialect(gooseDialect)

	if err := goose.Up(db, "."); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}
