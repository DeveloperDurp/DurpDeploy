package repository_test

import (
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"os"
	"testing"
)

func TestArtifactPinsSurviveRefreshAndRerun(t *testing.T) {
	// Given
	f := newArtifactFixture(t)
	release, err := handler.CreateReleaseSnapshot(
		t.Context(),
		f.repo,
		f.project.ID,
		"1.2.3",
	)
	if err != nil {
		t.Fatal(err)
	}
	args := db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: f.environment.ID,
		Status:        "pending",
	}
	first, err := f.repo.CreateDeployment(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	download, err := f.repo.DownloadDeploymentArtifact(
		t.Context(),
		first.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(download.Path); err != nil {
		t.Fatal(err)
	}
	original, err := f.repo.Queries.GetDeploymentArtifact(
		t.Context(),
		first.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: first.Deployment.ID, Status: "succeeded",
		}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SelectArtifactRepository(
		t.Context(),
		db.SelectProjectArtifactRepositoryParams{ProjectID: f.project.ID},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.RefreshReleaseSnapshot(
		t.Context(),
		f.repo,
		release,
	); err != nil {
		t.Fatalf("used release refresh=%v", err)
	}
	// When
	rerun, err := f.repo.CreateDeploymentFromDeployment(
		t.Context(),
		args,
		first.Deployment.ID,
	)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	pin, err := f.repo.Queries.GetDeploymentArtifact(
		t.Context(),
		rerun.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Url != original.Url || pin.Sha256 != original.Sha256 ||
		pin.Size != original.Size ||
		pin.Version != "1.2.3" {
		t.Fatalf("pin changed: %+v", pin)
	}
}
