package dispatch

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/pgdriver"
	"durpdeploy/internal/repository"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type lifecycleEngine struct {
	name   string
	dsn    string
	driver string
}

func provisionLifecycleEngine(t *testing.T, name string) lifecycleEngine {
	t.Helper()
	switch name {
	case "SQLite":
		return lifecycleEngine{
			name: name,
			dsn: "file:" + filepath.Join(t.TempDir(), "lifecycle.db") +
				"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
				"&_pragma=journal_mode(WAL)",
			driver: "sqlite",
		}
	case "PostgreSQL":
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		container, err := postgres.Run(ctx, "postgres:16-alpine",
			postgres.WithDatabase("lifecycle"),
			postgres.WithUsername("lifecycle"),
			postgres.WithPassword("fixture-only"),
			postgres.BasicWaitStrategies())
		if err != nil {
			t.Fatalf("start required PostgreSQL backend: %v", err)
		}
		t.Cleanup(func() {
			if err := container.Terminate(context.Background()); err != nil {
				t.Errorf("remove PostgreSQL container: %v", err)
			}
		})
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		return lifecycleEngine{name, dsn, pgdriver.DriverName}
	case "SQLServer":
		return provisionSQLServerLifecycleEngine(t)
	default:
		t.Fatalf("unknown lifecycle engine %q", name)
		return lifecycleEngine{}
	}
}

func provisionSQLServerLifecycleEngine(t *testing.T) lifecycleEngine {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	const password = "LifecycleFixtureOnly!123"
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
	if err != nil {
		t.Fatalf("start required SQL Server backend: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("remove SQL Server container: %v", err)
		}
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := (&url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword("sa", password),
		Host:   fmt.Sprintf("%s:%s", host, port.Port()),
	}).String() + "?database=master&encrypt=false&trustservercertificate=true"
	probe, err := sql.Open(mssqldriver.DriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	for {
		if err := probe.PingContext(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for SQL Server login: %v", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	return lifecycleEngine{"SQLServer", dsn, mssqldriver.DriverName}
}

func openLifecycleEngine(
	t *testing.T,
	engine lifecycleEngine,
) (*repository.Repository, *repository.Repository) {
	t.Helper()
	primary, err := migrate.Run(engine.dsn)
	if err != nil {
		t.Fatalf("migrate %s: %v", engine.name, err)
	}
	secondary, err := sql.Open(engine.driver, engine.dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondary.Ping(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := secondary.Close(); err != nil {
			t.Error(err)
		}
		if err := primary.Close(); err != nil {
			t.Error(err)
		}
	})
	return repository.New(primary), repository.New(secondary)
}

func seedExactDeadlineClaim(t *testing.T, repo *repository.Repository) {
	t.Helper()
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	pin := strings.Repeat("a", 64)
	if _, err := repo.DB.ExecContext(ctx, `INSERT INTO agents
		(id,name,endpoint,status,certificate_pem,certificate_fingerprint,
		 encrypted_identity) VALUES(?,?,?,?,?,?,?)`, "race-agent", "race-agent",
		"https://agent", "active", "cert", pin, "cipher"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx, `INSERT INTO agent_pairings
		(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
		 server_public_identity,server_pin,encrypted_identity,state,
		 expires_at,paired_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "race-agent",
		make([]byte, 32), "agent", pin, "server", pin, "cipher", "paired",
		int64(9_999_999_999), int64(1)); err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: environment.ID, Status: "pending",
			AssignedAgentID: sql.NullString{String: "race-agent", Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.Queries.CreateRemoteDeploymentClaim(ctx,
		deployment.ID); err != nil || changed != 1 {
		t.Fatalf("create claim rows=%d error=%v", changed, err)
	}
	now, err := repo.Queries.CurrentUnixTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.ClaimRemoteDeployment(ctx,
		db.ClaimRemoteDeploymentParams{
			ClaimTokenHash: bytes.Repeat([]byte{1}, 32),
			Ciphertext:     sql.NullString{String: "ciphertext", Valid: true},
			ClaimExpiresAt: now, Now: now,
			DeploymentID: deployment.ID, AgentID: "race-agent",
		}); err != nil || changed != 0 {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.DB.ExecContext(ctx, `UPDATE remote_deployment_claims SET
		state='claimed',claim_token_hash=?,ciphertext=?,claim_expires_at=?,
		last_heartbeat_at=?,updated_at=? WHERE deployment_id=?`,
		bytes.Repeat([]byte{1}, 32), "ciphertext", now, now, now,
		deployment.ID); err != nil {
		t.Fatal(err)
	}
}
