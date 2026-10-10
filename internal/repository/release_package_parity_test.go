package repository

import (
	"testing"

	"durpdeploy/internal/db"
)

func TestReleasePackageOmissionAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		project, err := repo.Queries.CreateProject(t.Context(),
			db.CreateProjectParams{Name: "omitted-package"})
		if err != nil {
			t.Fatal(err)
		}
		release, err := repo.Queries.CreateRelease(t.Context(),
			db.CreateReleaseParams{ProjectID: project.ID, Version: "2.0",
				StepsJson: "[]", PackageOmitted: 1})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := repo.Queries.GetDeploymentRelease(
			t.Context(),
			release.ID,
		)
		if err != nil || stored.PackageOmitted != 1 {
			t.Fatalf("stored=%+v error=%v", stored, err)
		}
		updated, err := repo.Queries.UpdateRelease(t.Context(),
			db.UpdateReleaseParams{ID: release.ID, ProjectID: project.ID,
				Version: "2.0", StepsJson: "[]", PackageOmitted: 0})
		if err != nil || updated.PackageOmitted != 0 {
			t.Fatalf("updated=%+v error=%v", updated, err)
		}
		_, err = repo.Queries.CreateRelease(t.Context(),
			db.CreateReleaseParams{ProjectID: project.ID, Version: "invalid",
				StepsJson: "[]", PackageOmitted: 2})
		if err == nil {
			t.Fatal("invalid omission state accepted")
		}
	})
}
