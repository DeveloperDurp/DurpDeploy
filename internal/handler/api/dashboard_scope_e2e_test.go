//go:build e2e

package api_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestDashboardProjectScopeAPIWebE2E(t *testing.T) {
	// Given: two projects share an environment but have different memberships.
	f := newArtifactE2E(t)
	projects := []db.Project{f.project, seedProject(t, f.h.repo)}
	statuses := [][]string{
		{"running", "queued", "succeeded"},
		{"publishing_artifact", "pending_approval", "failed"},
	}
	var deployments [][]db.Deployment
	for i, project := range projects {
		release, err := f.h.repo.Queries.CreateRelease(t.Context(),
			db.CreateReleaseParams{
				ProjectID: project.ID, Version: "scoped", StepsJson: "[]",
			})
		if err != nil {
			t.Fatal(err)
		}
		var rows []db.Deployment
		for _, status := range statuses[i] {
			rows = append(rows, seedDeployment(t, f.h.repo,
				release.ID, f.environment.ID, status))
		}
		deployments = append(deployments, rows)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET created_at = ?", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name        string
		role        string
		memberships []int
	}{
		{"admin", "admin", nil},
		{"first project", "viewer", []int{0}},
		{"second project", "viewer", []int{1}},
		{"both projects", "viewer", []int{0, 1}},
		{"no membership", "viewer", nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			user := seedAPIUser(
				t,
				f.h.repo,
				strings.ReplaceAll(
					fixture.name,
					" ",
					"-",
				)+"@scope.example",
				fixture.role,
			)
			visible := fixture.memberships
			if fixture.role == "admin" {
				visible = []int{0, 1}
			}
			for _, i := range fixture.memberships {
				if err := f.h.repo.Queries.AddProjectMember(t.Context(),
					db.AddProjectMemberParams{
						ProjectID: projects[i].ID, UserID: user.ID, Role: "deployer",
					}); err != nil {
					t.Fatal(err)
				}
			}
			_, f.token = seedAPIToken(t, f.h.repo, user.ID)
			var err error
			f.session, f.csrf, err = auth.NewSessionToken()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.repo.Queries.CreateSession(t.Context(),
				db.CreateSessionParams{
					ID: f.session, UserID: user.ID, CsrfToken: f.csrf,
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				}); err != nil {
				t.Fatal(err)
			}
			// When: the same identity loads the page, refresh, list and activity API.
			page := dashboardDocument(t, f.web(t, "GET", "/", nil, 200))
			refresh := dashboardDocument(t,
				f.web(t, "GET", "/dashboard/deployments", nil, 200))
			var listed struct {
				Data  []db.Deployment `json:"items"`
				Total int             `json:"total"`
			}
			if err := json.Unmarshal(f.api(t, "GET", "/api/v1/deployments",
				nil, 200), &listed); err != nil {
				t.Fatal(err)
			}
			var activity []struct {
				Counts map[string]int `json:"counts"`
			}
			if err := json.Unmarshal(f.api(t, "GET", "/api/v1/deployments/activity",
				nil, 200), &activity); err != nil {
				t.Fatal(err)
			}
			// Then: rows, statuses and counts agree with project access, not environment access.
			allowed := make(map[int64]string)
			wantedStatus := make(map[string]int)
			wantedRows := map[string]map[int64]string{
				"home-running": {}, "home-waiting": {},
				"home-latest": {}, "home-recent": {},
			}
			var recentIDs []int64
			for _, i := range visible {
				for j, deployment := range deployments[i] {
					allowed[deployment.ID] = deployment.Status
					wantedStatus[deployment.Status]++
					recentIDs = append(recentIDs, deployment.ID)
					section := []string{"home-running", "home-waiting", "home-latest"}[j]
					wantedRows[section][deployment.ID] = deployment.Status
				}
			}
			slices.Sort(recentIDs)
			slices.Reverse(recentIDs)
			for _, id := range recentIDs[:min(5, len(recentIDs))] {
				wantedRows["home-recent"][id] = allowed[id]
			}
			if listed.Total != len(allowed) ||
				len(listed.Data) != len(allowed) {
				t.Fatalf("API deployment total/data=%d/%d, want %d",
					listed.Total, len(listed.Data), len(allowed))
			}
			for _, row := range listed.Data {
				if allowed[row.ID] != row.Status {
					t.Fatalf("API exposed unexpected deployment %d", row.ID)
				}
			}
			actualStatus := make(map[string]int)
			for _, day := range activity {
				for status, count := range day.Counts {
					actualStatus[status] += count
				}
			}
			if !reflect.DeepEqual(actualStatus, wantedStatus) {
				t.Fatalf(
					"activity counts=%v, want %v",
					actualStatus,
					wantedStatus,
				)
			}
			assertDashboardRows(t, page, wantedRows)
			assertDashboardRows(t, refresh, wantedRows)
			for _, metric := range []struct {
				id    string
				count int
			}{
				{"home-running-count", len(visible)},
				{"home-today-count", len(allowed)},
			} {
				container := dashboardElements(page, "id", metric.id)
				if len(container) != 1 {
					t.Fatalf("missing metric %s", metric.id)
				}
				want := strconv.Itoa(metric.count)
				if dashboardCount(
					t,
					dashboardElements(container[0], "class", "stat-value"),
				) != want ||
					dashboardCount(t, dashboardElements(refresh, "hx-swap-oob",
						"innerHTML:#"+metric.id+" .stat-value")) != want {
					t.Fatalf(
						"initial/refresh metric %s differs from %s",
						metric.id,
						want,
					)
				}
			}
			summary := dashboardElements(page, "data-home-summary", "")
			if len(summary) != 1 {
				t.Fatal("dashboard summary missing")
			}
			counts := dashboardElements(summary[0], "class", "stat-value")
			if len(counts) != 4 ||
				dashboardText(counts[0]) != strconv.Itoa(len(visible)) ||
				dashboardText(counts[1]) != "1" {
				t.Fatal(
					"project count must be scoped and the shared environment count must remain global",
				)
			}
		})
	}
}
