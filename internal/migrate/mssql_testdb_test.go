package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newSQLServerTestDB(t *testing.T) *sql.DB {
	return newSQLServerTestDBAt(t, 0)
}

func newSQLServerTestDBAt(t *testing.T, version int64) *sql.DB {
	t.Helper()

	ctx := context.Background()
	const password = "Durpdeploy12345!"
	ctr, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "mcr.microsoft.com/mssql/server:2022-latest",
				ExposedPorts: []string{"1433/tcp"},
				Env: map[string]string{
					"ACCEPT_EULA":       "Y",
					"MSSQL_SA_PASSWORD": password,
				},
				WaitingFor: wait.ForListeningPort("1433/tcp").
					WithStartupTimeout(3 * time.Minute),
			},
			Started: true,
		},
	)
	requireNoError(t, err, "start SQL Server container")
	t.Cleanup(func() {
		if err := ctr.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	requireNoError(t, err, "get container host")
	port, err := ctr.MappedPort(ctx, "1433/tcp")
	requireNoError(t, err, "get mapped port")
	dsn := (&url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword("sa", password),
		Host:   fmt.Sprintf("%s:%s", host, port.Port()),
	}).String() + "?database=master&encrypt=false&trustservercertificate=true"

	config, err := migrationConfig(dsn)
	requireNoError(t, err, "build SQL Server migration config")

	var dbConn *sql.DB
	for attempt := 0; attempt < 15; attempt++ {
		dbConn, err = sql.Open(config.driverName, config.dsn)
		if err == nil {
			err = dbConn.Ping()
		}
		if err == nil {
			goose.SetBaseFS(config.migrationFS)
			if err = goose.SetDialect(config.gooseDialect); err == nil {
				if version == 0 {
					err = goose.Up(dbConn, ".")
				} else {
					err = goose.UpTo(dbConn, ".", version)
				}
			}
		}
		if err == nil {
			break
		}
		if dbConn != nil {
			_ = dbConn.Close()
		}
		time.Sleep(2 * time.Second)
	}
	requireNoError(t, err, "run SQL Server migrations")
	t.Cleanup(func() {
		if err := dbConn.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return dbConn
}
