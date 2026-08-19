package agentserver

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func newRuntimeParityFixture(t *testing.T, dsn string) *pollFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	serverIdentity := loadTestIdentity(t)
	agentIdentity := loadTestIdentity(t)
	conn, err := migrate.Run(dsn)
	if strings.HasPrefix(dsn, "sqlserver://") {
		for attempt := 0; err != nil && attempt < 14; attempt++ {
			time.Sleep(2 * time.Second)
			conn, err = migrate.Run(dsn)
		}
	}
	if err != nil {
		t.Fatalf("migrate runtime parity database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new test secret box: %v", err)
	}
	fixture := &enrollmentFixture{
		now: now, token: fixtureToken, repo: repository.New(conn),
		serverIdentity: serverIdentity, agentIdentity: agentIdentity, box: box,
	}
	listener, err := New(
		Config{Repository: fixture.repo, Identity: serverIdentity,
			Now: func() time.Time { return fixture.now }, Box: box},
	)
	if err != nil {
		t.Fatalf("new agent listener: %v", err)
	}
	listener.pollWait = func(context.Context) error { return nil }
	server := httptest.NewUnstartedServer(listener.Handler())
	server.TLS = listener.TLSConfig()
	server.StartTLS()
	t.Cleanup(server.Close)
	fixture.listener = listener
	fixture.server = server

	project, err := fixture.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "runtime-parity-project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := fixture.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "runtime-parity-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := fixture.repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "runtime-parity-v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	pool, err := fixture.repo.Queries.CreateAgentPool(context.Background(),
		db.CreateAgentPoolParams{Name: "runtime-parity-pool"})
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := fixture.repo.Queries.UpsertEnvironmentAgentPolicy(
		context.Background(),
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: environment.ID,
			PoolID:        pool.ID,
		},
	); err != nil {
		t.Fatalf("set environment policy: %v", err)
	}
	return &pollFixture{enrollmentFixture: fixture, poolID: pool.ID,
		poolName: pool.Name, releaseID: release.ID, envID: environment.ID}
}

func postgresRuntimeParityDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(
			"durpdeploy",
		),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("PostgreSQL runtime parity unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL runtime parity DSN: %v", err)
	}
	return dsn
}

func sqlServerRuntimeParityDSN(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	const password = "Durpdeploy12345!"
	container, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "mcr.microsoft.com/mssql/server:2022-latest", ExposedPorts: []string{"1433/tcp"},
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
	if err != nil {
		t.Skipf("SQL Server runtime parity unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("SQL Server runtime parity host: %v", err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatalf("SQL Server runtime parity port: %v", err)
	}
	return (&url.URL{Scheme: "sqlserver", User: url.UserPassword("sa", password),
		Host: fmt.Sprintf("%s:%s", host, port.Port())}).String() +
		"?database=master&encrypt=false&trustservercertificate=true"
}
