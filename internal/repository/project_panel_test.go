package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
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
	for range 6 {
		createProjectPanelDeployment(t, repo, 1, 1, 500, "failed")
	}
	checkProjectPanelBatch(t, repo, name, other.ID, environment.ID)
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

func TestDeploymentVisibilityAcrossEngines(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		userID := seedDeploymentVisibility(t, repo)
		for _, fixture := range []struct {
			name, token                    string
			user, admin, succeeded, failed int64
		}{
			{"admin", "", 0, 1, 1, 1},
			{"member", "", userID, 0, 1, 0},
			{"nonmember", "", 0, 0, 0, 0},
			{"scoped admin", "visibility", 0, 1, 1, 0},
			{"scoped member", "visibility", userID, 0, 1, 0},
			{"scope without membership", "visibility", 0, 0, 0, 0},
			{"missing grant", "unknown-token", userID, 1, 0, 0},
		} {
			t.Run(fixture.name, func(t *testing.T) {
				checkDeploymentVisibility(t, repo, fixture.token,
					fixture.user, fixture.admin,
					map[string]int64{"succeeded": fixture.succeeded,
						"failed": fixture.failed})
			})
		}
	})
}

func seedDeploymentVisibility(t *testing.T, repo *Repository) int64 {
	t.Helper()
	user, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "visibility@example.test", Name: "Visibility",
		PasswordHash: "test-only", Role: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: 1, UserID: user.ID,
			Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateApiToken(t.Context(),
		db.CreateApiTokenParams{ID: "visibility", UserID: user.ID,
			Name: "Visibility", TokenPrefix: "test", TokenHash: "test-only",
			Scope: "scoped"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.AddApiTokenProject(t.Context(),
		db.AddApiTokenProjectParams{TokenID: "visibility", ProjectID: 1},
	); err != nil {
		t.Fatal(err)
	}
	other, err := repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "foreign visibility"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{ProjectID: other.ID, Version: "foreign",
			StepsJson: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	createProjectPanelDeployment(t, repo, 1, 1, 100, "succeeded")
	createProjectPanelDeployment(t, repo, release.ID, 1, 200, "failed")
	return user.ID
}

func checkDeploymentVisibility(t *testing.T, repo *Repository,
	token string, user, admin int64, want map[string]int64,
) {
	t.Helper()
	total, err := repo.Queries.CountDeploymentsWithRefsFiltered(t.Context(),
		db.CountDeploymentsWithRefsFilteredParams{
			TokenID: token, UserID: user, IsAdmin: admin})
	if err != nil || total != want["succeeded"]+want["failed"] {
		t.Fatalf("visible count=%d want=%v error=%v", total, want, err)
	}
	rows, err := repo.Queries.ListDeploymentsWithRefsFiltered(t.Context(),
		db.ListDeploymentsWithRefsFilteredParams{
			TokenID: token, UserID: user, IsAdmin: admin, PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]int64{"succeeded": 0, "failed": 0}
	for _, row := range rows {
		listed[row.Status]++
	}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("visible list=%v want=%v", listed, want)
	}
	activity, err := repo.Queries.DeploymentActivity(t.Context(),
		db.DeploymentActivityParams{TokenID: token, UserID: user,
			IsAdmin: admin, FromUnix: 0, ToUnix: 86400})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{"succeeded": 0, "failed": 0}
	for _, row := range activity {
		counts[row.Status] += row.Count
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("visible activity=%v want=%v", counts, want)
	}
}
