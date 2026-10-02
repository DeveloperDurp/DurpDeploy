package repository

import (
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestProjectPackageSourceIdentityAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		// Given: a pinned case-sensitive source under each supported DB collation.
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		box, err := secret.NewBox(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		repo.SetSecretBox(box)
		project, err := repo.Queries.CreateProject(
			t.Context(),
			db.CreateProjectParams{Name: "source-identity"},
		)
		if err != nil {
			t.Fatal(err)
		}
		original := artifact.Repository{
			URLTemplate: "https://repo.example/App/{version}.zip",
			AuthType:    "basic",
			Username:    "Alice",
			Credential:  "original-secret",
		}
		source, err := repo.SaveProjectPackageRepository(
			t.Context(),
			project.ID,
			original,
		)
		if err != nil {
			t.Fatal(err)
		}
		release, err := repo.Queries.CreateRelease(
			t.Context(),
			db.CreateReleaseParams{
				ProjectID: project.ID,
				Version:   "1.0",
				StepsJson: "[]",
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
				Url:          "https://repo.example/App/1.0.zip",
				Version:      "1.0",
				Sha256:       "pinned",
				Size:         123,
			},
		); err != nil {
			t.Fatal(err)
		}
		// When: saving source identities that SQL Server may compare as equal.
		for _, changed := range []artifact.Repository{
			{URLTemplate: "https://repo.example/app/{version}.zip", AuthType: "basic", Username: "Alice", Credential: "new-secret"},
			{URLTemplate: original.URLTemplate, AuthType: "basic", Username: "alice", Credential: "new-secret"},
			{URLTemplate: original.URLTemplate, AuthType: "basic", Username: "Alice ", Credential: "new-secret"},
		} {
			current, err := repo.SaveProjectPackageRepository(
				t.Context(),
				project.ID,
				changed,
			)
			if err != nil {
				t.Fatal(err)
			}
			// Then: no different identity overwrites the pinned source or its secret.
			if current.ID == source.ID {
				t.Fatal("collation-equivalent identity reused a pinned source")
			}
			retained, err := repo.GetPackageRepository(t.Context(), source.ID)
			if err != nil || retained.UrlTemplate != original.URLTemplate ||
				retained.Username != original.Username ||
				retained.Credential != original.Credential {
				t.Fatal("historical source identity or credential changed")
			}
		}
		original.Credential = "rotated-secret"
		reactivated, err := repo.SaveProjectPackageRepository(
			t.Context(),
			project.ID,
			original,
		)
		if err != nil || reactivated.ID != source.ID {
			t.Fatal("byte-exact historical identity was not reactivated")
		}
	})
}
