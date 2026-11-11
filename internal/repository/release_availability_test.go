package repository

import (
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseAvailabilityAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, checkReleaseAvailabilityEngine)
}

func checkReleaseAvailabilityEngine(t *testing.T, name string) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, name))
	ctx := t.Context()
	p, err := repo.Queries.GetProject(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool, releaseID int64) {
		assertReleaseAvailability(t, repo, p, want, releaseID)
	}
	check(true, 1)
	createProjectPanelDeployment(t, repo, 1, 1, 100, "succeeded")
	check(false, 1)
	latest, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: p.ID, Version: "arbitrary-label", StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx,
		"UPDATE releases SET created_at = 10 WHERE project_id = ?", p.ID,
	); err != nil {
		t.Fatal(err)
	}
	check(true, latest.ID)
	createProjectPanelDeployment(t, repo, latest.ID, 1, 200, "failed")
	check(true, latest.ID)
	dry := createProjectPanelDeployment(t, repo,
		latest.ID, 1, 300, "succeeded")
	if _, err := repo.DB.ExecContext(ctx,
		"UPDATE deployments SET ansible_check_mode = 1 WHERE id = ?", dry.ID,
	); err != nil {
		t.Fatal(err)
	}
	check(true, latest.ID)
	createProjectPanelDeployment(t, repo, latest.ID, 1, 400, "succeeded")
	check(false, latest.ID)
	createProjectPanelDeployment(t, repo, 1, 1, 500, "succeeded")
	check(true, latest.ID)
	// A lifecycle without stages has no eligible targets.
	lc, err := repo.Queries.CreateLifecycle(ctx, db.CreateLifecycleParams{
		Name: "No targets",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(ctx,
		"UPDATE projects SET lifecycle_id = ? WHERE id = ?", lc.ID, p.ID,
	); err != nil {
		t.Fatal(err)
	}
	p, err = repo.Queries.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(false, latest.ID)
	empty, err := repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "No releases"})
	if err != nil {
		t.Fatal(err)
	}
	states, err := repo.ReleaseAvailability(ctx, []db.Project{empty})
	if err != nil || states[empty.ID].LatestRelease != nil ||
		states[empty.ID].UpdatesAvailable {
		t.Fatalf("empty project: %+v %v", states, err)
	}
}

func assertReleaseAvailability(
	t *testing.T,
	repo *Repository,
	project db.Project,
	want bool,
	releaseID int64,
) {
	t.Helper()
	states, err := repo.ReleaseAvailability(t.Context(), []db.Project{project})
	if err != nil {
		t.Fatal(err)
	}
	state := states[project.ID]
	if state.UpdatesAvailable != want || state.LatestRelease.ID != releaseID {
		t.Fatalf("release summary: %+v", state)
	}
	rows, total, err := repo.ProjectCatalog(t.Context(),
		ProjectCatalogAccess{IsAdmin: 1},
		ProjectCatalogOptions{
			Sort: "name", GroupID: -1, Limit: 1, UpdatesAvailable: true,
		})
	if err != nil || (total == 1) != want || (len(rows) == 1) != want {
		t.Fatalf("filter: %+v total=%d err=%v", rows, total, err)
	}
}
