// Package testutil provides isolated database fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	// Register the native driver for fixture readiness checks.
	_ "github.com/microsoft/go-mssqldb"
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
		"harbor.durp.info/microsoft/mssql/server:2022-latest",
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
	c, dsn, err := StartSQLServer(ctx, image,
		func(c testcontainers.Container) { CleanupContainer(t, c) })
	if err != nil {
		t.Fatalf("start required SQL Server backend: %v", err)
	}
	return c, dsn
}

// StartSQLServer registers ownership before checking the startup result.
// The caller must bound ctx and remove the registered container on every exit.
func StartSQLServer(
	ctx context.Context, image string,
	onCreated func(testcontainers.Container),
) (_ testcontainers.Container, _ string, result error) {
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
	onCreated(c)
	if err != nil {
		return c, "", err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, "", err
	}
	port, err := c.MappedPort(ctx, sqlServerPort)
	if err != nil {
		return c, "", err
	}
	dsn := (&url.URL{
		Scheme: "sqlserver", User: url.UserPassword("sa", password),
		Host: net.JoinHostPort(host, port.Port()),
	}).String() + "?database=master&encrypt=false&trustservercertificate=true"
	probe, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return c, "", err
	}
	defer func() {
		result = errors.Join(result, probe.Close())
	}()
	for {
		if err := probe.PingContext(ctx); err == nil {
			return c, dsn, nil
		}
		select {
		case <-ctx.Done():
			return c, "", fmt.Errorf("wait for SQL Server login: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
