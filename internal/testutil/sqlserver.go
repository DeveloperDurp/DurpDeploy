// Package testutil provides isolated database fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/mssqldriver"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SQLServerDSN owns a fresh container with a random password and host port.
// Only this container is terminated when the test completes.
func SQLServerDSN(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	password := "Fixture!" + rand.Text()
	container, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "mcr.microsoft.com/mssql/server:2022-latest",
				ExposedPorts: []string{"1433/tcp"},
				Env: map[string]string{
					"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password,
				},
				WaitingFor: wait.ForLog(
					"SQL Server is now ready for client connections",
				).WithStartupTimeout(3 * time.Minute),
			},
			Started: true,
		})
	CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start required SQL Server backend: %v", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{
		Scheme: "sqlserver", User: url.UserPassword("sa", password),
		Host: net.JoinHostPort(host, port.Port()),
	}).String() + "?database=master&encrypt=false&trustservercertificate=true"
	probe, err := sql.Open(mssqldriver.DriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	}()
	for {
		if err := probe.PingContext(ctx); err == nil {
			return dsn
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for SQL Server login: %v", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
