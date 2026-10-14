// Package testutil provides isolated database fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/mssqldriver"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const sqlServerPort = "1433/tcp"

// SQLServerDSN owns a fresh container with a random password and host port.
// Only this container is terminated when the test completes.
func SQLServerDSN(t *testing.T) string {
	t.Helper()
	_, dsn := SQLServerContainer(
		t,
		"mcr.microsoft.com/mssql/server:2022-latest",
	)
	return dsn
}

// SQLServerContainer exposes the owned fixture for native recovery operations.
func SQLServerContainer(
	t *testing.T,
	image string,
) (testcontainers.Container, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	password := "Fixture!" + rand.Text()
	c, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        image,
				ExposedPorts: []string{sqlServerPort},
				Env: map[string]string{
					"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": password,
				},
				HostConfigModifier: func(config *container.HostConfig) {
					config.PortBindings = network.PortMap{
						network.MustParsePort(sqlServerPort): {
							{HostIP: netip.MustParseAddr("127.0.0.1")},
						},
					}
				},
				WaitingFor: wait.ForLog(
					"SQL Server is now ready for client connections",
				).WithStartupTimeout(3 * time.Minute),
			},
			Started: true,
		})
	CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start required SQL Server backend: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, sqlServerPort)
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
			return c, dsn
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for SQL Server login: %v", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
