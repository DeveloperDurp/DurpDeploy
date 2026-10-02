package repository_test

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
)

func TestProjectPackageSaveEnablesSourceWithoutSelection(t *testing.T) {
	// Given: a project with no configured package.
	repo := newTestRepo(t)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "single-source"},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := artifact.Repository{
		URLTemplate: "https://repo.example/{version}.zip",
		AuthType:    "noauth",
	}
	// When: saving the singleton configuration.
	configured, err := repo.SaveProjectPackageRepository(
		t.Context(),
		project.ID,
		source,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Then: snapshots use that source without a separate selection operation.
	active, err := repo.Queries.GetProjectArtifactRepository(
		t.Context(),
		project.ID,
	)
	if err != nil || active.ID != configured.ID ||
		active.UrlTemplate != source.URLTemplate {
		t.Fatalf("source not activated: %+v (%v)", active, err)
	}
}

func TestProjectPackageReplacementPreservesOldDeploymentSource(t *testing.T) {
	// Given: a release and deployment pinned to the original authenticated source.
	f := newArtifactFixture(t)
	release, err := handler.CreateReleaseSnapshot(
		t.Context(),
		f.repo,
		f.project.ID,
		"1.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := f.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: f.environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	oldPin, err := f.repo.Queries.GetDeploymentArtifact(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	newSource := artifact.Repository{
		URLTemplate: "https://replacement.example/{version}.zip",
		AuthType:    "noauth",
	}
	// When: replacing the project's source URL and authentication type.
	active, err := f.repo.SaveProjectPackageRepository(
		t.Context(),
		f.project.ID,
		newSource,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Then: one new source is active, while old pins and credentials still work.
	if active.ID == f.source.ID {
		t.Fatal("pinned source was overwritten")
	}
	retained, err := f.repo.GetPackageRepository(t.Context(), f.source.ID)
	if err != nil || retained.UrlTemplate != f.source.UrlTemplate ||
		retained.Credential != "credential" {
		t.Fatalf("historical source changed: %+v (%v)", retained, err)
	}
	pin, err := f.repo.Queries.GetDeploymentArtifact(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || pin != oldPin {
		t.Fatal("replacement changed deployment pin")
	}
	download, err := f.repo.DownloadDeploymentArtifact(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(download.Path)
	if download.SHA256 != oldPin.Sha256 || download.Size != oldPin.Size {
		t.Fatal("old pinned package no longer resolves")
	}
}

func TestProjectPackageBlankSecretCannotFollowReplacement(t *testing.T) {
	// Given: an authenticated source with a saved credential.
	f := newArtifactFixture(t)
	source := artifact.Repository{
		URLTemplate: "https://other.example/{version}.zip",
		AuthType:    "bearer",
	}
	// When: replacing its destination without explicitly supplying credentials.
	_, err := f.repo.SaveProjectPackageRepository(
		t.Context(),
		f.project.ID,
		source,
	)
	// Then: it rejects the replacement and preserves the active source.
	if !errors.Is(err, artifact.ErrInvalid) {
		t.Fatalf("old secret could follow new destination: %v", err)
	}
	active, err := f.repo.Queries.GetProjectArtifactRepository(
		t.Context(),
		f.project.ID,
	)
	if err != nil || active.ID != f.source.ID {
		t.Fatal("invalid replacement changed active source")
	}
}

func TestProjectPackageRemoveRetainsPinnedCredentials(t *testing.T) {
	// Given: a project source referenced by an immutable release.
	f := newArtifactFixture(t)
	if _, err := handler.CreateReleaseSnapshot(
		t.Context(),
		f.repo,
		f.project.ID,
		"1.0",
	); err != nil {
		t.Fatal(err)
	}
	// When: disabling packages for future snapshots.
	if err := f.repo.RemoveProjectPackageRepository(
		t.Context(),
		f.project.ID,
	); err != nil {
		t.Fatal(err)
	}
	// Then: no source is active but historical release credentials are retained.
	if _, err := f.repo.Queries.GetProjectArtifactRepository(
		t.Context(),
		f.project.ID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("source is still active: %v", err)
	}
	if _, err := f.repo.GetPackageRepository(
		t.Context(),
		f.source.ID,
	); err != nil {
		t.Fatal("pinned source was deleted")
	}
}
