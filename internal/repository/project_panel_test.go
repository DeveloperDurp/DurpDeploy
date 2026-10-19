package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectEnvironmentDeploymentHistory(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		checkProjectEnvironmentDeploymentHistory(t, name)
	})
}

func checkProjectEnvironmentDeploymentHistory(t *testing.T, name string) {
	t.Helper()
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, name))
	other, err := repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "other project"})
	if err != nil {
		t.Fatal(err)
	}
	otherRelease, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: other.ID,
			Version:   "other",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var own, foreign []db.Deployment
	for _, status := range []string{
		"succeeded", "failed", "succeeded", "cancelled", "failed", "failed",
	} {
		own = append(
			own,
			createProjectPanelDeployment(t, repo, 1, 1, 100, status),
		)
	}
	for range 6 {
		foreign = append(
			foreign,
			createProjectPanelDeployment(
				t,
				repo,
				otherRelease.ID,
				1,
				200,
				"succeeded",
			),
		)
	}
	runbook := createProjectPanelDeployment(t, repo, 1, 1, 300, "succeeded")
	if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET kind = 'runbook' WHERE id = ?", runbook.ID,
	); err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "other environment"})
	if err != nil {
		t.Fatal(err)
	}
	createProjectPanelDeployment(t, repo, 1, environment.ID, 400, "succeeded")
	for _, fixture := range []projectPanelHistory{{1, own[2].ID, own}, {other.ID, foreign[5].ID, foreign}} {
		t.Run(fmt.Sprint(fixture.project), func(t *testing.T) {
			checkProjectPanelHistory(t, repo, fixture)
			checkProjectPanelPagination(t, repo, fixture)
		})
	}
	_, err = repo.Queries.GetLatestSuccessfulDeploymentForProjectEnv(
		t.Context(), db.GetLatestSuccessfulDeploymentForProjectEnvParams{
			ProjectID: other.ID + 1, EnvironmentID: 1,
		})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty project success: %v", err)
	}
	recent, err := repo.Queries.ListRecentDeploymentsForProjectEnv(
		t.Context(), db.ListRecentDeploymentsForProjectEnvParams{
			ProjectID: other.ID + 1, EnvironmentID: 1, Limit: 5,
		})
	if err != nil || len(recent) != 0 {
		t.Fatalf("empty project recent: %+v, %v", recent, err)
	}
}

type projectPanelHistory struct {
	project, success int64
	attempts         []db.Deployment
}

func checkProjectPanelHistory(
	t *testing.T,
	repo *Repository,
	fixture projectPanelHistory,
) {
	t.Helper()
	latest, err := repo.Queries.GetLatestSuccessfulDeploymentForProjectEnv(
		t.Context(),
		db.GetLatestSuccessfulDeploymentForProjectEnvParams{
			ProjectID: fixture.project, EnvironmentID: 1,
		},
	)
	if err != nil || latest.ID != fixture.success {
		t.Fatalf("latest success=%d want=%d error=%v",
			latest.ID, fixture.success, err)
	}
	recent, err := repo.Queries.ListRecentDeploymentsForProjectEnv(
		t.Context(), db.ListRecentDeploymentsForProjectEnvParams{
			ProjectID: fixture.project, EnvironmentID: 1, Limit: 5,
		})
	if err != nil || len(recent) != 5 {
		t.Fatalf("recent count=%d error=%v", len(recent), err)
	}
	for i, d := range recent {
		if want := fixture.attempts[len(fixture.attempts)-1-i].ID; d.ID != want {
			t.Fatalf("recent[%d]=%d want=%d", i, d.ID, want)
		}
	}
}

func checkProjectPanelPagination(
	t *testing.T,
	repo *Repository,
	fixture projectPanelHistory,
) {
	t.Helper()
	for page := range 2 {
		rows, err := repo.Queries.ListDeploymentsWithRefsFiltered(
			t.Context(), db.ListDeploymentsWithRefsFilteredParams{
				IsAdmin: 1,
				FProjectID: sql.NullInt64{
					Int64: fixture.project,
					Valid: true,
				},
				FEnvID:    sql.NullInt64{Int64: 1, Valid: true},
				PageLimit: 3, PageOffset: int64(page * 3),
			})
		if err != nil || len(rows) != 3 {
			t.Fatalf(
				"history page %d count=%d error=%v",
				page,
				len(rows),
				err,
			)
		}
		for i, d := range rows {
			want := fixture.attempts[len(fixture.attempts)-1-page*3-i].ID
			if d.ID != want {
				t.Fatalf(
					"history page %d row %d=%d want=%d",
					page,
					i,
					d.ID,
					want,
				)
			}
		}
	}
}

func createProjectPanelDeployment(
	t *testing.T,
	repo *Repository,
	releaseID, envID, timestamp int64,
	status string,
) db.Deployment {
	t.Helper()
	d, err := repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: releaseID, EnvironmentID: envID, Status: status,
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET created_at = ? WHERE id = ?", timestamp, d.ID,
	); err != nil {
		t.Fatal(err)
	}
	return d
}
