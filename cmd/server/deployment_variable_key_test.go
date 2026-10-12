package main

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func seedVariableRotationSnapshot(
	t *testing.T,
	repo *repository.Repository,
	box *secret.Box,
) db.DeploymentVariableSnapshot {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "rotation"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "rotation"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "rotation", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	value, err := box.Encrypt("deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.CreateDeploymentVariableSnapshot(
		t.Context(),
		db.CreateDeploymentVariableSnapshotParams{
			DeploymentID: deployment.ID, Value: value,
		},
	); err != nil {
		t.Fatal(err)
	}
	return db.DeploymentVariableSnapshot{
		DeploymentID: deployment.ID,
		Value:        value,
	}
}
