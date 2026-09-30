package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestReleaseDeleteRemovesCompletedHistoryAndSchedules(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "delete-release@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	other := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	ctx := context.Background()
	deployment, err := h.repo.Queries.CreateDeployment(ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "succeeded",
		})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.DB.ExecContext(ctx,
		"INSERT INTO deployment_logs (deployment_id, line) VALUES (?, ?)",
		deployment.ID, "completed log")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.DB.ExecContext(ctx, `INSERT INTO scheduled_deployments
(project_id, release_id, environment_id, cron, next_run_at, enabled)
VALUES (?, ?, ?, '0 9 * * *', 9999999999, 0)`,
		project.ID, release.ID, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	path := fmt.Sprintf("/api/v1/projects/%d/releases/%d",
		project.ID, release.ID)

	// A release from another project must remain untouched.
	wrong := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf(
			"/api/v1/projects/%d/releases/%d",
			other.ID,
			release.ID,
		),
		nil,
	)
	wrong.Header.Set("Authorization", "Bearer "+token)
	wrongResponse := httptest.NewRecorder()
	router.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-project delete=%d", wrongResponse.Code)
	}

	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d: %s", response.Code, response.Body.String())
	}
	for _, table := range []string{
		"releases", "scheduled_deployments", "deployments", "deployment_logs",
	} {
		var count int
		if err := h.repo.DB.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+table,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows=%d err=%v", table, count, err)
		}
	}
	// A second delete reports a missing release, not another success.
	again := httptest.NewRecorder()
	router.ServeHTTP(again, req)
	if again.Code != http.StatusNotFound {
		t.Fatalf("repeat delete=%d", again.Code)
	}
	for _, path := range []string{
		"/api/v1/projects/invalid/releases/1",
		fmt.Sprintf("/api/v1/projects/%d/releases/invalid", project.ID),
	} {
		bad := httptest.NewRequest(http.MethodDelete, path, nil)
		bad.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, bad)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("DELETE %s: status=%d", path, result.Code)
		}
	}
}

func TestReleaseDeleteBlocksActiveDeployment(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "active-release@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	deployment, err := h.repo.Queries.CreateDeployment(context.Background(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		})
	if err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	path := fmt.Sprintf("/api/v1/projects/%d/releases/%d",
		project.ID, release.ID)
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusConflict {
		t.Fatalf("active delete=%d: %s", response.Code, response.Body.String())
	}
	if _, err := h.repo.Queries.GetDeployment(
		context.Background(), deployment.ID,
	); err != nil {
		t.Fatalf("active deployment lost: %v", err)
	}
}
