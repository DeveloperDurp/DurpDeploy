package repository_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type artifactFixture struct {
	repo        *repository.Repository
	project     db.Project
	environment db.Environment
	source      db.PackageRepository
}

func newArtifactFixture(t *testing.T) artifactFixture {
	t.Helper()
	repo := newTestRepo(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entry, err := writer.Create("app.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("package")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer credential" {
				w.WriteHeader(401)
				return
			}
			if _, err := w.Write(data.Bytes()); err != nil {
				t.Error(err)
			}
		}),
	)
	t.Cleanup(server.Close)
	repo.ArtifactClient = &artifact.Client{HTTP: server.Client()}
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "artifact-project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "artifact-env"},
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   project.ID,
			Name:        "packages",
			UrlTemplate: server.URL + "/{version}.zip",
			AuthType:    "bearer",
			Credential:  "credential",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SelectArtifactRepository(
		t.Context(),
		db.SelectProjectArtifactRepositoryParams{
			ProjectID:    project.ID,
			RepositoryID: source.ID,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "check",
			ScriptBody:     "true",
			ContainerImage: "alpine:3.21",
			VariableNames:  "[]",
		},
	); err != nil {
		t.Fatal(err)
	}
	return artifactFixture{repo, project, environment, source}
}

func TestArtifactRepositoryCredentialsAreEncrypted(t *testing.T) {
	// Given / When
	f := newArtifactFixture(t)
	stored, err := f.repo.Queries.GetPackageRepository(t.Context(), f.source.ID)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if stored.Credential == "credential" || stored.Credential == "" {
		t.Fatal("credential not encrypted")
	}
	plain, err := f.repo.GetPackageRepository(t.Context(), stored.ID)
	if err != nil || plain.Credential != "credential" {
		t.Fatalf("credential roundtrip failed: %v", err)
	}
}

func TestRunbookCopiesSelectedReleaseArtifact(t *testing.T) {
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
	// When
	_, version, err := f.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID:         f.project.ID,
			Name:              "maintenance",
			ArtifactReleaseID: release.ID,
			StepsJSON:         release.StepsJson,
		},
	)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	pin, err := f.repo.Queries.GetReleaseArtifact(
		t.Context(),
		version.ReleaseID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Version != "1.2.3" || pin.SourceReleaseID.Int64 != release.ID {
		t.Fatalf("wrong source pin: %+v", pin)
	}
}

func TestArtifactDeploymentRejectsRemoteSteps(t *testing.T) {
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
	if _, err := f.repo.Queries.UpdateRelease(
		t.Context(),
		db.UpdateReleaseParams{
			ID:        release.ID,
			ProjectID: f.project.ID,
			Version:   release.Version,
			StepsJson: `[{"name":"remote","execution_target":"agent","script_body":"true"}]`,
		},
	); err != nil {
		t.Fatal(err)
	}
	// When
	_, err = f.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: f.environment.ID,
			Status:        "pending",
		},
	)
	// Then
	if !errors.Is(err, repository.ErrRemoteArtifactsUnsupported) {
		t.Fatalf("remote artifact accepted: %v", err)
	}
}
