package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestArtifactPinsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		// Given
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		project, err := repo.Queries.CreateProject(
			t.Context(),
			db.CreateProjectParams{Name: "artifact-parity"},
		)
		if err != nil {
			t.Fatal(err)
		}
		source, err := repo.CreatePackageRepository(
			t.Context(),
			db.CreatePackageRepositoryParams{
				ProjectID:   project.ID,
				Name:        "packages",
				UrlTemplate: "https://repo.example/{version}.zip",
				AuthType:    "noauth",
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
		release, err := repo.Queries.CreateRelease(
			t.Context(),
			db.CreateReleaseParams{
				ProjectID: project.ID,
				Version:   "1.2.3",
				StepsJson: `[{"name":"read","script_body":"true","container_image":"bash:5.2"}]`,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Queries.CreateReleaseArtifact(
			t.Context(),
			db.CreateReleaseArtifactParams{
				ReleaseID:    release.ID,
				RepositoryID: source.ID,
				Url:          "https://repo.example/1.2.3.zip",
				Version:      "1.2.3",
				Sha256:       "pinned",
				Size:         123,
			},
		); err != nil {
			t.Fatal(err)
		}
		// When
		deployment, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID:     release.ID,
				EnvironmentID: 1,
				Status:        "pending",
			},
		)
		// Then
		if err != nil {
			t.Fatal(err)
		}
		pin, err := repo.Queries.GetDeploymentArtifact(
			t.Context(),
			deployment.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if pin.Sha256 != "pinned" || pin.Version != "1.2.3" || pin.Size != 123 {
			t.Fatalf("pin changed: %+v", pin)
		}
		if err := repo.DeletePackageRepository(
			t.Context(),
			db.DeletePackageRepositoryParams{
				ID:        source.ID,
				ProjectID: project.ID,
			},
		); !errors.Is(
			err,
			ErrArtifactRepositoryPinned,
		) {
			t.Fatalf("pinned repository deleted: %v", err)
		}
		if _, err := repo.UpdatePackageRepository(
			t.Context(),
			db.UpdatePackageRepositoryParams{
				ID:          source.ID,
				ProjectID:   project.ID,
				Name:        "renamed",
				UrlTemplate: source.UrlTemplate,
				AuthType:    "noauth",
			},
		); err != nil {
			t.Fatal(err)
		}
		allowed, err := repo.Queries.HasLiveArtifactClaim(
			t.Context(),
			db.HasLiveArtifactClaimParams{
				DeploymentID:   deployment.Deployment.ID,
				AgentID:        "missing",
				ClaimTokenHash: []byte("missing"),
				Now:            sql.NullInt64{Int64: 1, Valid: true},
				StaleBefore:    sql.NullInt64{Int64: 0, Valid: true},
			},
		)
		if err != nil || allowed != 0 {
			t.Fatalf("missing claim accepted: %d %v", allowed, err)
		}
		if err := repo.DeleteProject(
			t.Context(),
			project.ID,
		); !errors.Is(
			err,
			ErrProjectHasActiveRunbook,
		) {
			t.Fatalf("active deployment deletion was not blocked: %v", err)
		}
		if err := repo.Queries.UpdateDeploymentStatus(
			t.Context(),
			db.UpdateDeploymentStatusParams{
				ID:     deployment.Deployment.ID,
				Status: "failed",
			},
		); err != nil {
			t.Fatal(err)
		}
		_, version, err := repo.SaveRunbook(
			t.Context(),
			RunbookSave{
				ProjectID:         project.ID,
				Name:              "retained-package",
				StepsJSON:         release.StepsJson,
				ArtifactReleaseID: release.ID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.DeleteRelease(
			t.Context(),
			project.ID,
			release.ID,
		); err != nil {
			t.Fatalf("artifact release deletion: %v", err)
		}
		retained, err := repo.Queries.GetReleaseArtifact(
			t.Context(),
			version.ReleaseID,
		)
		if err != nil || retained.Sha256 != "pinned" ||
			retained.SourceReleaseID.Valid {
			t.Fatalf("runbook pin not retained: %+v %v", retained, err)
		}
		if err := repo.DeleteProject(t.Context(), project.ID); err != nil {
			t.Fatalf("artifact project deletion: %v", err)
		}
	})
}
