package repository

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/pgdriver"
	"durpdeploy/internal/testutil"
)

type deploymentCreationEngine struct {
	name   string
	dsn    string
	driver string
}

func forEachDeploymentCreationEngine(
	t *testing.T,
	test func(*testing.T, string),
) {
	t.Helper()
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			test(t, name)
		})
	}
}

func newDeploymentCreationEngine(
	t *testing.T,
	name string,
) deploymentCreationEngine {
	t.Helper()
	switch name {
	case "SQLite":
		return deploymentCreationEngine{
			name: name,
			dsn: "file:" + filepath.Join(t.TempDir(), "creation.db") +
				"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
				"&_pragma=journal_mode(WAL)",
			driver: "sqlite",
		}
	case "PostgreSQL":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		container, err := postgres.Run(
			ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("creation"),
			postgres.WithUsername("creation"),
			postgres.WithPassword("fixture-only"),
			postgres.BasicWaitStrategies(),
		)
		testutil.CleanupContainer(t, container)
		if err != nil {
			t.Fatalf("start required PostgreSQL backend: %v", err)
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		return deploymentCreationEngine{
			name: name, dsn: dsn, driver: pgdriver.DriverName,
		}
	case "SQLServer":
		return newSQLServerDeploymentCreationEngine(t)
	default:
		t.Fatalf("unknown database engine %q", name)
		return deploymentCreationEngine{}
	}
}

func newSQLServerDeploymentCreationEngine(
	t *testing.T,
) deploymentCreationEngine {
	t.Helper()
	return deploymentCreationEngine{
		name: "SQLServer", dsn: testutil.SQLServerDSN(t),
		driver: mssqldriver.DriverName,
	}
}

func openDeploymentCreationEngine(
	t *testing.T,
	engine deploymentCreationEngine,
) (*Repository, *Repository) {
	t.Helper()
	primary, err := migrate.Run(engine.dsn)
	if err != nil {
		t.Fatalf("migrate %s: %v", engine.name, err)
	}
	secondary, err := sql.Open(engine.driver, engine.dsn)
	if err != nil {
		t.Fatalf("open second %s connection: %v", engine.name, err)
	}
	if err := secondary.Ping(); err != nil {
		t.Fatalf("ping second %s connection: %v", engine.name, err)
	}
	t.Cleanup(func() {
		if err := secondary.Close(); err != nil {
			t.Error(err)
		}
		if err := primary.Close(); err != nil {
			t.Error(err)
		}
	})
	first, second := New(primary), New(secondary)
	seedDeploymentCreationRace(t, first)
	return first, second
}

func seedDeploymentCreationRace(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "creation-race"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "creation-race"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		ctx,
		`INSERT INTO agents
(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity)
VALUES (?,?,?,?,?,?,?)`,
		"race-agent",
		"race-agent",
		"https://agent.invalid",
		"active",
		"certificate",
		strings.Repeat("a", 64),
		"cipher",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx, `INSERT INTO agent_pairings
(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
 server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`, "race-agent", make([]byte, 32), "public",
		strings.Repeat("b", 64), "server", strings.Repeat("c", 64), "cipher",
		"paired", int64(500), int64(100)); err != nil {
		t.Fatal(err)
	}
}

func createLegacyRemoteDeployment(
	t *testing.T,
	repo *Repository,
) db.Deployment {
	t.Helper()
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{
			Name: fmt.Sprintf("legacy-%d", time.Now().UnixNano()),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: environment.ID, Status: "pending",
			AssignedAgentID: sql.NullString{
				String: "race-agent",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.CreateEnvironmentDeploymentSlot(t.Context(),
		db.CreateEnvironmentDeploymentSlotParams{
			EnvironmentID: environment.ID, DeploymentID: deployment.ID,
		}); err != nil {
		t.Fatal(err)
	}
	created, err := repo.Queries.CreateRemoteDeploymentClaim(
		t.Context(),
		deployment.ID,
	)
	if err != nil || created != 1 {
		t.Fatalf("create legacy claim rows=%d error=%v", created, err)
	}
	return deployment
}

func TestCreateDeploymentFromDeploymentPreservesSourceAcrossDatabases(
	t *testing.T,
) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		const original = `[` +
			`{"name":"bash","script_body":"echo original",` +
			`"interpreter":"bash","execution_target":"local",` +
			`"container_image":"alpine:3.20"},` +
			`{"name":"python","script_body":"print('original')",` +
			`"interpreter":"python3","execution_target":"local",` +
			`"container_image":"python:3.13-alpine"}]`
		if _, err := repo.Queries.UpdateRelease(
			t.Context(),
			db.UpdateReleaseParams{
				ID: 1, ProjectID: 1, Version: "v1", StepsJson: original,
			},
		); err != nil {
			t.Fatal(err)
		}
		source, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Queries.UpdateRelease(
			t.Context(),
			db.UpdateReleaseParams{
				ID:        1,
				ProjectID: 1,
				Version:   "v1",
				StepsJson: `[{"name":"powershell","script_body":"Write-Output refreshed",` +
					`"interpreter":"pwsh","execution_target":"local",` +
					`"container_image":"mcr.microsoft.com/powershell:7.5-alpine-3.20"}]`,
			},
		); err != nil {
			t.Fatal(err)
		}
		rerun, err := repo.CreateDeploymentFromDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending",
			},
			source.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		stepSource, err := repo.Queries.GetDeploymentStepSource(
			t.Context(),
			rerun.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if stepSource.StepsJson != original {
			t.Fatalf("rerun source = %s", stepSource.StepsJson)
		}
		rerunSteps, err := repo.Queries.ListDeploymentSteps(
			t.Context(),
			rerun.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		wantInterpreters := map[string]string{
			"bash": "bash", "python": "python3",
		}
		if len(rerunSteps) != len(wantInterpreters) {
			t.Fatalf("rerun steps = %+v", rerunSteps)
		}
		for _, step := range rerunSteps {
			if want := wantInterpreters[step.Name]; step.Interpreter != want {
				t.Fatalf(
					"rerun step %q interpreter=%q want %q",
					step.Name, step.Interpreter, want,
				)
			}
		}
	})
}
