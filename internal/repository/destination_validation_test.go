package repository

import (
	"errors"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func TestPackageSaveDestinationsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		project, err := repo.Queries.CreateProject(t.Context(),
			db.CreateProjectParams{Name: "destination-validation"})
		if err != nil {
			t.Fatal(err)
		}
		good := artifact.Repository{
			URLTemplate: "https://host.containers.internal/{version}.zip",
			AuthType:    "noauth",
		}
		saved, err := repo.SaveProjectPackageRepository(
			t.Context(),
			project.ID,
			good,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{
			"127.0.0.1", "LOCALHOST.", "2130706433", "0x7f.0.0.1",
			"0177.0.0.1", "169.254.169.254", "100.100.100.200",
		} {
			checkRejectedPackageDestination(
				t,
				repo,
				project.ID,
				saved,
				good,
				host,
			)
		}
	})
}

func checkRejectedPackageDestination(
	t *testing.T,
	repo *Repository,
	projectID int64,
	saved db.PackageRepository,
	good artifact.Repository,
	host string,
) {
	t.Helper()
	bad := good
	bad.URLTemplate = "https://" + host + "/{version}.zip"
	_, err := repo.SaveProjectPackageRepository(
		t.Context(),
		projectID,
		bad,
	)
	if !errors.Is(err, artifact.ErrInvalid) {
		t.Fatalf(
			"host %s: expected invalid destination, got %v",
			host,
			err,
		)
	}
	_, err = repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID: projectID, Name: "blocked", UrlTemplate: bad.URLTemplate,
			AuthType: "noauth",
		},
	)
	if !errors.Is(err, artifact.ErrInvalid) {
		t.Fatalf("create host %s: %v", host, err)
	}
	_, err = repo.UpdatePackageRepository(
		t.Context(),
		db.UpdatePackageRepositoryParams{
			ID: saved.ID, ProjectID: projectID, Name: "blocked",
			UrlTemplate: bad.URLTemplate, AuthType: "noauth",
		},
	)
	if !errors.Is(err, artifact.ErrInvalid) {
		t.Fatalf("update host %s: %v", host, err)
	}
	current, err := repo.Queries.GetProjectArtifactRepository(
		t.Context(),
		projectID,
	)
	if err != nil || current.ID != saved.ID ||
		current.UrlTemplate != saved.UrlTemplate {
		t.Fatalf("rejected save mutated source: %+v, %v", current, err)
	}
}
