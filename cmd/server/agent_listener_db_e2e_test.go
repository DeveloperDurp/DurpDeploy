package main

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestAgentListener_remoteAgentCompletesRemoteDispatch_Postgres(
	t *testing.T,
) {
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL DSN: %v", err)
	}
	testAgentListenerRemoteAgentCompletesRemoteDispatch(t, dsn)
}

func TestAgentListener_remoteAgentCompletesRemoteDispatch_MSSQL(t *testing.T) {
	ctx := context.Background()
	const password = "Durpdeploy12345!"
	dsnFor := func(host string, port network.Port) string {
		return (&url.URL{
			Scheme: "sqlserver",
			User:   url.UserPassword("sa", password),
			Host:   fmt.Sprintf("%s:%s", host, port.Port()),
		}).String() +
			"?database=master&encrypt=false&trustservercertificate=true"
	}
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
				WaitingFor: wait.ForSQL("1433/tcp", "sqlserver", dsnFor).
					WithStartupTimeout(3 * time.Minute),
			},
			Started: true,
		},
	)
	if err != nil {
		t.Fatalf("start SQL Server container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("SQL Server host: %v", err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatalf("SQL Server port: %v", err)
	}
	dsn := dsnFor(host, port)
	testAgentListenerRemoteAgentCompletesRemoteDispatch(t, dsn)
}
