package repository_test

import (
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"

	"github.com/testcontainers/testcontainers-go"
)

func TestDispatchDatabaseParity(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	for _, engine := range []string{"postgres", "mssql"} {
		t.Run(engine, func(t *testing.T) {
			backend := "PostgreSQL"
			if engine == "mssql" {
				backend = "SQLServer"
			}
			dsn := repository.RepositoryDatabaseDSN(t, backend)
			conn, err := migrate.Run(dsn)
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
			runAgentFleetDatabaseParity(t, r)
			runRemoteAtomicity(t, r)
			runRemoteStepStartEligibility(t, r)
			runProjectMembershipIntegerContract(t, r)
			runStagingStorageParity(t, r)
		})
	}
}
