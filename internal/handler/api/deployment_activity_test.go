package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestDeploymentActivityAPI(t *testing.T) {
	h := newAPIHarness(t)
	project := seedProject(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	env := seedEnv(t, h.repo)
	other := seedRelease(t, h.repo, seedProject(t, h.repo).ID)
	seedDeployment(t, h.repo, release.ID, env.ID, "succeeded")
	seedDeployment(t, h.repo, other.ID, env.ID, "failed")
	runbook := seedDeployment(t, h.repo, release.ID, env.ID, "succeeded")
	if err := h.repo.Queries.SetRunbookDeploymentKind(
		t.Context(), runbook.ID,
	); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, timestamp := range []int64{
		today.AddDate(0, 0, -13).Unix() - 1,
		today.AddDate(0, 0, 1).Unix(),
	} {
		old := seedDeployment(t, h.repo, release.ID, env.ID, "failed")
		if _, err := h.repo.DB.ExecContext(t.Context(),
			"UPDATE deployments SET created_at = ? WHERE id = ?",
			timestamp, old.ID,
		); err != nil {
			t.Fatal(err)
		}
	}
	first := seedDeployment(t, h.repo, release.ID, env.ID, "cancelled")
	if _, err := h.repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET created_at = ? WHERE id = ?",
		today.AddDate(0, 0, -13).Unix(), first.ID,
	); err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	for _, role := range []string{"admin", "viewer"} {
		t.Run(role, func(t *testing.T) {
			user := seedAPIUser(t, h.repo, role+"@chart.test", role)
			if err := h.repo.Queries.AddProjectMember(t.Context(),
				db.AddProjectMemberParams{
					ProjectID: project.ID, UserID: user.ID, Role: "deployer",
				}); err != nil {
				t.Fatal(err)
			}
			_, token := seedAPIToken(t, h.repo, user.ID)
			req := httptest.NewRequest(
				"GET",
				"/api/v1/deployments/activity",
				nil,
			)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
			}
			var days []struct {
				Date   string           `json:"date"`
				Counts map[string]int64 `json:"counts"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &days); err != nil {
				t.Fatal(err)
			}
			if len(days) != 14 || days[0].Counts["cancelled"] != 1 ||
				days[13].Counts["succeeded"] != 1 {
				t.Fatalf("wrong window/counts: %s", rec.Body.String())
			}
			var failed int64
			if role == "admin" {
				failed = 1
			}
			if days[13].Counts["failed"] != failed ||
				days[1].Counts == nil || len(days[1].Counts) != 0 {
				t.Fatalf("scope/zero-fill: %s", rec.Body.String())
			}
			for i, day := range days {
				want := today.AddDate(0, 0, i-13).Format(time.DateOnly)
				if day.Date != want {
					t.Fatalf("date %q, want %q", day.Date, want)
				}
			}
		})
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(
		"GET", "/api/v1/deployments/activity", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous activity: %d", rec.Code)
	}
}
