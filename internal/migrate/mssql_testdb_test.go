package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newSQLServerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

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
				WaitingFor: wait.ForLog("SQL Server is now ready for client connections").
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

	var dbConn *sql.DB
	for attempt := 0; attempt < 15; attempt++ {
		dbConn, err = Run(dsn)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	requireNoError(t, err, "run migrations")
	t.Cleanup(func() {
		if err := dbConn.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return dbConn
}
