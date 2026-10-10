package repository

import (
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func TestDeferredArtifactPinsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, checkDeferredArtifactPinsAcrossDatabase)
}

func checkDeferredArtifactPinsAcrossDatabase(t *testing.T, name string) {
	t.Helper()
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, name))
	project, err := repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "deferred-parity"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.SaveProjectPackageRepository(
		t.Context(),
		project.ID,
		artifact.Repository{
			URLTemplate: "https://unreachable.invalid/{version}.zip",
			AuthType:    "noauth",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "pending",
			StepsJson: `[{"name":"read","script_body":"true","container_image":"bash:5.2"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := repo.CaptureProjectArtifact(
		t.Context(),
		project.ID,
		release.Version,
	)
	if err != nil || pending == nil ||
		pending.Row.RepositoryID != source.ID {
		t.Fatalf("capture failed: %+v (%v)", pending, err)
	}
	if err := repo.WithTx(t.Context(), func(q *db.Queries) error {
		return pending.Insert(t.Context(), q, release.ID)
	}); err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: 1, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	row, err := repo.Queries.GetDeploymentArtifact(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || row.ResolutionKey != pending.Row.ResolutionKey ||
		row.Sha256 != "" || row.Size != 0 {
		t.Fatalf("pending copy changed: %+v (%v)", row, err)
	}
	if err := repo.resolveDeploymentArtifact(t.Context(), row,
		artifact.Download{SHA256: "validated", Size: 123}); err != nil {
		t.Fatal(err)
	}
	pinned, err := repo.Queries.GetReleaseArtifact(t.Context(), release.ID)
	if err != nil || pinned.Sha256 != "validated" || pinned.Size != 123 {
		t.Fatalf("release did not resolve: %+v (%v)", pinned, err)
	}
	deploymentPin, err := repo.Queries.GetDeploymentArtifact(
		t.Context(),
		row.DeploymentID,
	)
	if err != nil || deploymentPin.Sha256 != pinned.Sha256 ||
		deploymentPin.Size != pinned.Size {
		t.Fatalf("deployment did not resolve: %+v (%v)", deploymentPin, err)
	}
}
