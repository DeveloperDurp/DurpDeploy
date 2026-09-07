package api_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
)

func TestRootOnlyDeployment_ListsAndCounts(t *testing.T) {
	// Given
	h, root, _ := fanoutSurfaceFixture(t)
	for _, path := range []string{"/deployments", "/projects/1/deployments"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			if strings.HasPrefix(path, "/projects") {
				req = withAPIURLParam(req, "id", "1")
			}
			rec := httptest.NewRecorder()
			// When
			api.NewDeploymentHandler(h.repo, nil).ListDeployments(rec, req)
			// Then
			var result struct {
				Items []db.Deployment `json:"items"`
				Total int64           `json:"total"`
			}
			mustDecode(t, rec.Body, &result)
			if rec.Code != 200 || result.Total != 1 || len(result.Items) != 1 ||
				result.Items[0].ID != root.ID {
				t.Fatalf("root list: %#v status %d", result, rec.Code)
			}
		})
	}
	rows, err := h.repo.Queries.ListDeploymentsByRelease(
		context.Background(),
		root.ReleaseID,
	)
	if err != nil || len(rows) != 1 || rows[0].ID != root.ID {
		t.Fatalf("release history: %#v %v", rows, err)
	}
}

func TestRootOnlyLifecycleHistory_ChildSuccessDoesNotPromote(t *testing.T) {
	// Given
	h, root, children := fanoutSurfaceFixture(t)
	if _, err := h.repo.DB.Exec("UPDATE deployments SET status='succeeded', created_at=created_at+1 WHERE id=?", children[0].ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// When
	_, err := h.repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
		ctx,
		db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID:     root.ReleaseID,
			EnvironmentID: root.EnvironmentID,
		},
	)
	// Then
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("child success opened promotion gate: %v", err)
	}
	latest, err := h.repo.Queries.GetLatestDeploymentForReleaseEnv(
		ctx,
		db.GetLatestDeploymentForReleaseEnvParams{
			ReleaseID:     root.ReleaseID,
			EnvironmentID: root.EnvironmentID,
		},
	)
	if err != nil || latest.ID != root.ID {
		t.Fatalf("latest history: %#v %v", latest, err)
	}
	history, err := h.repo.Queries.ListRecentDeploymentsForEnv(
		ctx,
		db.ListRecentDeploymentsForEnvParams{
			EnvironmentID: root.EnvironmentID,
			Limit:         100,
		},
	)
	if err != nil || len(history) != 1 || history[0].ID != root.ID {
		t.Fatalf("environment history: %#v %v", history, err)
	}
}

func TestRootOnlyDashboard_HidesChildren(t *testing.T) {
	// Given
	h, _, _ := fanoutSurfaceFixture(t)
	if _, err := h.repo.DB.Exec("UPDATE deployments SET status='failed' WHERE parent_deployment_id IS NOT NULL"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	// When
	handler.NewIndexHandler(h.repo).
		Index(rec, httptest.NewRequest("GET", "/", nil))
	// Then
	if rec.Code != 200 {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body)
	}
	if strings.Count(rec.Body.String(), "<tr>") != 6 ||
		strings.Contains(rec.Body.String(), "badge-error") {
		t.Fatal("dashboard must show one root in each of the three tables")
	}
	count, err := h.repo.Queries.CountDeploymentsToday(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("today count: %d %v", count, err)
	}
}
