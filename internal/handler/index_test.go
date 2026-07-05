package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestIndex_DashboardMetrics(t *testing.T) {
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer dbConn.Close()

	repo := repository.New(dbConn)
	q := repo.Queries
	ctx := context.Background()

	proj, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name:        "demo",
		Description: sql.NullString{String: "demo project", Valid: true},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	env, err := q.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name:        "staging",
		Description: sql.NullString{String: "staging env", Valid: true},
		Tags:        sql.NullString{String: "stage", Valid: true},
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}

	release, err := q.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID,
		Version:   "1.0.0",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}

	// 1 running + 2 succeeded
	if _, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "running",
		StartedAt:     sql.NullInt64{Int64: 1, Valid: true},
		FinishedAt:    sql.NullInt64{Valid: false},
		Forced:        0,
		Note:          sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create running deployment: %v", err)
	}
	if _, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "succeeded",
		StartedAt:     sql.NullInt64{Int64: 2, Valid: true},
		FinishedAt:    sql.NullInt64{Int64: 3, Valid: true},
		Forced:        0,
		Note:          sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create succeeded deployment #1: %v", err)
	}
	if _, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "succeeded",
		StartedAt:     sql.NullInt64{Int64: 4, Valid: true},
		FinishedAt:    sql.NullInt64{Int64: 5, Valid: true},
		Forced:        0,
		Note:          sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create succeeded deployment #2: %v", err)
	}

	h := handler.NewIndexHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	mustContain := []string{
		"demo",               // project name appears in running + latest-per rows
		"1.0.0",              // release version
		"staging",            // environment name
		"Currently running",  // section title
		"Latest per release", // section title
		"Deployments today",  // stat title
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
