package repository_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
)

func TestOldDeferredPullCannotReplaceRefreshedReleaseChecksum(t *testing.T) {
	f := newArtifactFixture(t)
	release, err := handler.CreateReleaseSnapshot(
		t.Context(),
		f.repo,
		f.project.ID,
		"same-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	args := db.CreateDeploymentParams{ReleaseID: release.ID,
		EnvironmentID: f.environment.ID, Status: "pending"}
	old, err := f.repo.CreateDeployment(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     old.Deployment.ID,
			Status: "failed",
		},
	); err != nil {
		t.Fatal(err)
	}
	payload := deferredZIP(t, "refreshed")
	upstream := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, err := w.Write(payload); err != nil {
				t.Error(err)
			}
		}),
	)
	t.Cleanup(upstream.Close)
	f.repo.ArtifactClient = &artifact.Client{
		HTTP:    upstream.Client(),
		TempDir: t.TempDir(),
	}
	if _, err := f.repo.SaveProjectPackageRepository(
		t.Context(),
		f.project.ID,
		artifact.Repository{
			URLTemplate: upstream.URL + "/{version}.zip",
			AuthType:    "noauth",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.RefreshReleaseSnapshot(
		t.Context(),
		f.repo,
		release,
	); err != nil {
		t.Fatal(err)
	}
	refreshed, err := f.repo.Queries.GetReleaseArtifact(t.Context(), release.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.repo.CreateDeploymentFromDeployment(
		t.Context(),
		args,
		old.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	download, err := f.repo.DownloadDeploymentArtifact(
		t.Context(),
		retry.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(download.Path); err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.Queries.GetReleaseArtifact(t.Context(), release.ID)
	if err != nil || current != refreshed || current.Sha256 == download.SHA256 {
		t.Fatalf(
			"old generation replaced refreshed pin: %+v (%v)",
			current,
			err,
		)
	}
	oldPin, err := f.repo.Queries.GetDeploymentArtifact(
		t.Context(),
		old.Deployment.ID,
	)
	if err != nil || oldPin.Sha256 != download.SHA256 ||
		oldPin.Size != download.Size {
		t.Fatalf("old generation was not pinned: %+v (%v)", oldPin, err)
	}
}
