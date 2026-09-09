package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"durpdeploy/migrations"
)

type remoteClaimTestDB struct {
	name            string
	dsn             string
	driverName      string
	gooseDialect    string
	baselineVersion int64
}

func forEachRemoteClaimDatabase(
	t *testing.T,
	test func(*testing.T, remoteClaimTestDB),
) {
	t.Helper()
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			test(t, newRemoteClaimTestDB(t, name))
		})
	}
}

func newRemoteClaimTestDB(t *testing.T, name string) remoteClaimTestDB {
	t.Helper()
	switch name {
	case "SQLite":
		return remoteClaimTestDB{
			name: name,
			dsn: filepath.Join(t.TempDir(), "claims.db") +
				"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
			driverName: "sqlite", gooseDialect: "sqlite3", baselineVersion: 27,
		}
	case "PostgreSQL":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		container, err := postgres.Run(
			ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("claims"),
			postgres.WithUsername("claims"),
			postgres.WithPassword("fixture-only"),
			postgres.BasicWaitStrategies(),
		)
		requireNoError(t, err, "start required PostgreSQL backend")
		t.Cleanup(func() {
			requireNoError(
				t,
				container.Terminate(context.Background()),
				"remove PostgreSQL container",
			)
		})
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		requireNoError(t, err, "PostgreSQL connection string")
		return remoteClaimTestDB{
			name: name, dsn: dsn, driverName: "pgx-qmark",
			gooseDialect: "postgres", baselineVersion: 27,
		}
	case "SQLServer":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		const password = "ClaimFixtureOnly!123"
		container, err := testcontainers.GenericContainer(
			ctx,
			testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{
					Image:        "mcr.microsoft.com/mssql/server:2022-latest",
					ExposedPorts: []string{"1433/tcp"},
					Env: map[string]string{
						"ACCEPT_EULA":       "Y",
						"MSSQL_SA_PASSWORD": password,
					},
					WaitingFor: wait.ForLog(
						"SQL Server is now ready for client connections",
					).WithStartupTimeout(3 * time.Minute),
				},
				Started: true,
			},
		)
		requireNoError(t, err, "start required SQL Server backend")
		t.Cleanup(func() {
			requireNoError(
				t,
				container.Terminate(context.Background()),
				"remove SQL Server container",
			)
		})
		host, err := container.Host(ctx)
		requireNoError(t, err, "SQL Server host")
		port, err := container.MappedPort(ctx, "1433/tcp")
		requireNoError(t, err, "SQL Server port")
		dsn := (&url.URL{
			Scheme: "sqlserver",
			User:   url.UserPassword("sa", password),
			Host:   fmt.Sprintf("%s:%s", host, port.Port()),
		}).String() + "?database=master&encrypt=false&trustservercertificate=true"
		config, err := migrationConfig(dsn)
		requireNoError(t, err, "SQL Server migration config")
		return remoteClaimTestDB{
			name: name, dsn: dsn, driverName: config.driverName,
			gooseDialect: "mssql", baselineVersion: 12,
		}
	default:
		t.Fatalf("unknown database %q", name)
		return remoteClaimTestDB{}
	}
}

func (fixture remoteClaimTestDB) openBaseline(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open(fixture.driverName, fixture.dsn)
	requireNoError(t, err, "open "+fixture.name)
	if fixture.gooseDialect == "mssql" {
		for attempt := 0; attempt < 15; attempt++ {
			err = conn.Ping()
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
	} else {
		err = conn.Ping()
	}
	requireNoError(t, err, "ping "+fixture.name)
	if fixture.gooseDialect == "mssql" {
		config, configErr := migrationConfig(fixture.dsn)
		requireNoError(t, configErr, "SQL Server migration config")
		goose.SetBaseFS(config.migrationFS)
	} else {
		goose.SetBaseFS(migrations.FS)
	}
	requireNoError(t, goose.SetDialect(fixture.gooseDialect), "set dialect")
	requireNoError(
		t,
		goose.UpTo(conn, ".", fixture.baselineVersion),
		"migrate "+fixture.name+" baseline",
	)
	return conn
}

func (fixture remoteClaimTestDB) openRaw(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open(fixture.driverName, fixture.dsn)
	requireNoError(t, err, "reopen "+fixture.name)
	requireNoError(t, conn.Ping(), "reping "+fixture.name)
	return conn
}
