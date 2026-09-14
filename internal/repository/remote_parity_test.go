package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestDispatchDatabaseParity(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	for _, engine := range []string{"postgres", "mssql"} {
		t.Run(engine, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Minute,
			)
			defer cancel()
			var ctr testcontainers.Container
			var dsn string
			if engine == "postgres" {
				pg, err := postgres.Run(
					ctx,
					"postgres:16-alpine",
					postgres.WithDatabase(
						"task3",
					),
					postgres.WithUsername("task3"),
					postgres.WithPassword(
						"fixture-only",
					),
					postgres.BasicWaitStrategies(),
				)
				if err != nil {
					t.Fatal(err)
				}
				ctr = pg
				dsn, err = pg.ConnectionString(ctx, "sslmode=disable")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				const password = "Task3FixtureOnly!123"
				var err error
				ctr, err = testcontainers.GenericContainer(ctx,
					testcontainers.GenericContainerRequest{
						ContainerRequest: testcontainers.ContainerRequest{
							Image:        "mcr.microsoft.com/mssql/server:2022-latest",
							ExposedPorts: []string{"1433/tcp"},
							Env:          map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password},
							WaitingFor:   wait.ForLog("SQL Server is now ready for client connections").WithStartupTimeout(2 * time.Minute),
						}, Started: true,
					})
				if err != nil {
					t.Fatal(err)
				}
				host, err := ctr.Host(ctx)
				if err != nil {
					t.Fatal(err)
				}
				port, err := ctr.MappedPort(ctx, "1433/tcp")
				if err != nil {
					t.Fatal(err)
				}
				dsn = (&url.URL{Scheme: "sqlserver", User: url.UserPassword("sa", password),
					Host:     fmt.Sprintf("%s:%s", host, port.Port()),
					RawQuery: "database=master&encrypt=false&trustservercertificate=true",
				}).String()
			}
			t.Cleanup(func() {
				if err := ctr.Terminate(context.Background()); err != nil {
					t.Error(err)
				}
				t.Log("cleanup: isolated database container removed")
			})
			var conn *sql.DB
			var err error
			for attempt := 0; attempt < 15; attempt++ {
				conn, err = migrate.Run(dsn)
				if err == nil || engine != "mssql" {
					break
				}
				time.Sleep(2 * time.Second)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			r := repository.New(conn)
			seedRemoteFixture(t, r)
			runRemoteAtomicity(t, r)
			runProjectMembershipIntegerContract(t, r)
		})
	}
}
