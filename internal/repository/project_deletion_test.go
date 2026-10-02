package repository_test

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestProjectDeletionHandlesNormalDeploymentSnapshots(t *testing.T) {
	for _, status := range []string{"pending", "running", "pending_approval", "succeeded", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			// Given
			repo, project := seedProjectDeletion(t, status)
			// When
			err := repo.DeleteProject(t.Context(), project.ID)
			// Then
			switch status {
			case "pending", "running", "pending_approval":
				if !errors.Is(err, repository.ErrProjectHasActiveRunbook) {
					t.Fatalf("active deletion not blocked: %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("snapshot deletion failed: %v", err)
				}
			}
		})
	}
}

func seedProjectDeletion(
	t *testing.T,
	status string,
) (*repository.Repository, db.Project) {
	t.Helper()
	repo := newTestRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "delete-project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "delete-env"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1",
			StepsJson: `[{"name":"normal","script_body":"true","container_image":"bash:5.2"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(
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
	if err := repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     deployment.Deployment.ID,
			Status: status,
		},
	); err != nil {
		t.Fatal(err)
	}
	return repo, project
}
