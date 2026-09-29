package runner

import (
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunnerLogsUnavailablePodmanBeforeExecution(t *testing.T) {
	r, repo, _ := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
esac
`)
	r.localErr = errors.New("execution runtime unavailable")
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "preflight"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1",
			StepsJson: `[{"name":"prepare","script_body":"echo never",` +
				`"execution_target":"local","container_image":"docker.io/library/bash:5.2"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	r.Run(t.Context(), deployment.Deployment.ID, release.ID, env.ID)
	var line string
	if err := repo.DB.QueryRowContext(
		t.Context(),
		"SELECT line FROM deployment_logs WHERE deployment_id=?",
		deployment.Deployment.ID,
	).Scan(&line); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "execution runtime unavailable") {
		t.Fatalf("preflight error not persisted in deployment log: %q", line)
	}
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		deployment.Deployment.ID,
	)
	if err != nil || stored.Status != "failed" || !stored.FinishedAt.Valid {
		t.Fatalf("deployment status=%q finished=%v: %v", stored.Status,
			stored.FinishedAt.Valid, err)
	}
}
