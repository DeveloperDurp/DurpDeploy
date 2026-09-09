package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"durpdeploy/internal/mssqldriver"
	_ "durpdeploy/internal/pgdriver"
	"durpdeploy/migrations"
)

var ErrAmbiguousAgentAssignments = errors.New(
	"multiple agents are assigned to one environment",
)

// Run opens the database and applies all pending migrations. A sqlserver://
// DSN uses internal/mssqldriver with the embedded native SQL Server migrations.
// PostgreSQL DSNs use internal/pgdriver, and all other DSNs are SQLite paths.
func Run(dsn string) (*sql.DB, error) {
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
	return runMigrations(dsn, "sqlite", "sqlite3", migrations.FS)
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
	return strings.HasPrefix(
		strings.ToLower(strings.TrimSpace(dsn)),
		"sqlserver://",
	)
}

func dsnWithSecureDefaults(dsn string) string {
	parsedDSN := strings.TrimSpace(dsn)
	u, err := url.Parse(parsedDSN)
	if err != nil {
		return parsedDSN
	}
	q := u.Query()
	if !hasQueryOption(q, "encrypt") {
		q.Set("encrypt", "true")
	}
	if !hasQueryOption(q, "trustservercertificate") {
		q.Set("trustservercertificate", "false")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func hasQueryOption(values url.Values, key string) bool {
	for existing := range values {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

func isPostgres(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") ||
		strings.HasPrefix(dsn, "postgresql://") ||
		strings.Contains(dsn, "host=") ||
		strings.Contains(dsn, "user=")
}

func run(dsn, driverName, gooseDialect string) (*sql.DB, error) {
	return runMigrations(dsn, driverName, gooseDialect, migrations.FS)
}

func runMigrations(
	dsn, driverName, gooseDialect string, migrationFS fs.FS,
) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect(gooseDialect); err != nil {
		db.Close()
		return nil, fmt.Errorf("set Goose dialect: %w", err)
	}
	version, err := goose.GetDBVersion(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("get migration version: %w", err)
	}
	if assignmentSchemaExists(gooseDialect, version) {
		if err := refuseAmbiguousAgentAssignments(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := goose.Up(db, "."); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

func assignmentSchemaExists(dialect string, version int64) bool {
	if dialect == "mssql" {
		return version >= 10 && version < 13
	}
	return version >= 25 && version < 28
}

func refuseAmbiguousAgentAssignments(db *sql.DB) error {
	rows, err := db.Query(`SELECT environment_id
		FROM environment_agent_assignments
		GROUP BY environment_id HAVING COUNT(*) > 1
		ORDER BY environment_id`)
	if err != nil {
		return fmt.Errorf("inspect environment agent assignments: %w", err)
	}
	defer rows.Close()

	var environmentIDs []string
	for rows.Next() {
		var environmentID int64
		if err := rows.Scan(&environmentID); err != nil {
			return fmt.Errorf("read ambiguous environment assignment: %w", err)
		}
		environmentIDs = append(
			environmentIDs,
			strconv.FormatInt(environmentID, 10),
		)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list ambiguous environment assignments: %w", err)
	}
	if len(environmentIDs) > 0 {
		return fmt.Errorf(
			"%w: environment IDs %s",
			ErrAmbiguousAgentAssignments,
			strings.Join(environmentIDs, ","),
		)
	}
	return nil
}
