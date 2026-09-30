package repository_test

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestCreateDeploymentRejectsLegacyServerStep(t *testing.T) {
	repo := newContainerSnapshotRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "legacy"},
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
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{"name":"old","script_body":"echo old","execution_target":"local"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
	})
	if !errors.Is(err, repository.ErrLegacyServerStep) {
		t.Fatalf("create legacy deployment error = %v", err)
	}
	var count int
	if err := repo.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM deployments").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy release created %d deployments", count)
	}
}

func TestCreateDeploymentKeepsAgentOnlyHistoryRunnable(t *testing.T) {
	repo := newContainerSnapshotRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "agent"},
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
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{"name":"remote","script_body":"echo remote","execution_target":"agent"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deployment.ID == 0 {
		t.Fatal("agent-only release did not create a deployment")
	}
}
