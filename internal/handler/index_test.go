package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
)

// Seed one succeeded row (dashboard-invisible history), one unresolved
// failed row (attention), one newer pending approval that takes the failed
// row's place under the project+environment takeover rule, and one running
// row (in-flight section).
func TestIndex_DashboardSections(t *testing.T) {
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer testutil.Close(t, dbConn)

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

	seed := func(status string, note sql.NullString) {
		t.Helper()
		if _, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        status,
			StartedAt:     sql.NullInt64{Int64: 1, Valid: true},
			FinishedAt:    sql.NullInt64{Valid: false},
			Forced:        0,
			Note:          note,
		}); err != nil {
			t.Fatalf("create %s deployment: %v", status, err)
		}
	}
	seed("succeeded", sql.NullString{Valid: false})
	seed("failed", sql.NullString{String: "first run", Valid: true})
	seed("pending_approval", sql.NullString{String: "Re-run", Valid: true})
	seed("running", sql.NullString{Valid: false})

	h := handler.NewIndexHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = auth.SetUser(req, &db.User{ID: 1, Role: "admin"})
	rec := httptest.NewRecorder()
	h.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	mustContain := []string{
		"demo", // project name in attention + running rows
		"1.0.0",
		"staging",
		"Needs attention",
		"Running",
		"pending_approval",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	hidden := []string{
		"Deployments today", // stat cards live on /statistics now
		"Latest per release",
		"Recent deployments",
		"id=\"home-latest\"",
		"id=\"home-recent\"",
		"data-home-charts", // charts live on /statistics now
	}
	for _, unwanted := range hidden {
		if strings.Contains(body, unwanted) {
			t.Errorf("body contains %q", unwanted)
		}
	}

	rec = httptest.NewRecorder()
	h.RefreshDeployments(
		rec,
		auth.SetUser(
			httptest.NewRequest(http.MethodGet, "/dashboard/deployments", nil),
			&db.User{ID: 1, Role: "admin"},
		),
	)
	if rec.Code != http.StatusOK ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"running refresh status/cache=%d/%q",
			rec.Code,
			rec.Header().Get("Cache-Control"),
		)
	}
	fragment := rec.Body.String()
	for _, want := range []string{
		"demo",
		`id="home-deployments"`,
		`id="home-attention"`,
		`id="home-running"`,
		`hx-trigger="every 5s, home-deployments-refreshed from:body"`,
	} {
		if !strings.Contains(fragment, want) {
			t.Errorf("running refresh missing %q", want)
		}
	}
	if strings.Contains(fragment, "<html") ||
		strings.Contains(fragment, "data-home-charts") {
		t.Fatal("running refresh rendered the full page")
	}
}

func TestStatisticsPage(t *testing.T) {
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer testutil.Close(t, dbConn)

	h := handler.NewIndexHandler(repository.New(dbConn))
	req := httptest.NewRequest(http.MethodGet, "/statistics", nil)
	req = auth.SetUser(req, &db.User{ID: 1, Role: "admin"})
	rec := httptest.NewRecorder()
	h.Statistics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected 200, got %d (body=%q)",
			rec.Code,
			rec.Body.String(),
		)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Statistics",
		"Projects",
		"Environments",
		"Deployments today",
		"data-home-charts",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("statistics body missing %q", want)
		}
	}
}
