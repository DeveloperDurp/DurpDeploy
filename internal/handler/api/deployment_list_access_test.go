package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

func TestGlobalDeploymentList_ProjectAccess(t *testing.T) {
	// Given two projects with matching deployment filters.
	h := newAPIHarness(t)
	owned, foreign := seedProject(t, h.repo), seedProject(t, h.repo)
	env, foreignEnv := seedEnv(t, h.repo), seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, owned.ID)
	foreignRelease := seedRelease(t, h.repo, foreign.ID)
	pending := seedDeployment(t, h.repo, release.ID, env.ID, "pending")
	seedDeployment(t, h.repo, release.ID, env.ID, "failed")
	seedDeployment(t, h.repo, foreignRelease.ID, foreignEnv.ID, "pending")
	seedDeployment(t, h.repo, foreignRelease.ID, foreignEnv.ID, "failed")
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))

	for _, role := range []string{"viewer", "deployer", "admin", "nonmember"} {
		t.Run(role, func(t *testing.T) {
			userRole := role
			if role == "nonmember" {
				userRole = "deployer"
			}
			user := seedAPIUser(t, h.repo, role+"@list.test", userRole)
			if role != "admin" && role != "nonmember" {
				err := h.repo.Queries.AddProjectMember(context.Background(),
					db.AddProjectMemberParams{
						ProjectID: owned.ID, UserID: user.ID, Role: "deployer",
					})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, token := seedAPIToken(t, h.repo, user.ID)
			for _, value := range []string{"0", "-1", "bad", "9223372036854775808"} {
				t.Run("invalid-project-"+value, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet,
						"/api/v1/deployments?project_id="+value, nil)
					req.Header.Set("Authorization", "Bearer "+token)
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want 400", rec.Code)
					}
				})
			}
			cases := []struct {
				name, query string
				total, rows int
			}{
				{"all", "", 2, 2},
				{"page", "?limit=1", 2, 1},
				{"offset", "?limit=1&offset=2", 2, 0},
				{"project", fmt.Sprintf("?project_id=%d", owned.ID), 2, 2},
				{"foreign", fmt.Sprintf("?project_id=%d", foreign.ID), 0, 0},
				{"nonexistent", "?project_id=999999", 0, 0},
				{"environment", fmt.Sprintf("?env_id=%d", foreignEnv.ID), 0, 0},
				{"status", "?status=pending", 1, 1},
				{"dates", "?from=0&to=9999999999", 2, 2},
				{
					"combined",
					fmt.Sprintf(
						"?project_id=%d&env_id=%d&status=pending&from=0&to=9999999999",
						owned.ID,
						env.ID,
					),
					1,
					1,
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					wantTotal, wantRows := tc.total, tc.rows
					if role == "nonmember" {
						wantTotal, wantRows = 0, 0
					} else if role == "admin" {
						switch tc.name {
						case "all", "dates":
							wantTotal, wantRows = 4, 4
						case "page", "offset":
							wantTotal, wantRows = 4, 1
						case "foreign", "environment":
							wantTotal, wantRows = 2, 2
						case "status":
							wantTotal, wantRows = 2, 2
						}
					}
					// When calling the public route with a real bearer credential.
					req := httptest.NewRequest(http.MethodGet,
						"/api/v1/deployments"+tc.query, nil)
					req.Header.Set("Authorization", "Bearer "+token)
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					// Then both rows and totals have the authorized scope.
					if rec.Code != http.StatusOK {
						t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
					}
					var result struct {
						Items []db.ListDeploymentsWithRefsFilteredRow `json:"items"`
						Total int                                     `json:"total"`
					}
					if err := json.Unmarshal(
						rec.Body.Bytes(),
						&result,
					); err != nil {
						t.Fatal(err)
					}
					if result.Total != wantTotal ||
						len(result.Items) != wantRows {
						t.Fatalf(
							"total/rows = %d/%d, want %d/%d",
							result.Total,
							len(result.Items),
							wantTotal,
							wantRows,
						)
					}
					if role != "admin" &&
						strings.Contains(rec.Body.String(), foreign.Name) {
						t.Fatal("foreign project metadata exposed")
					}
					if tc.name == "combined" && role != "nonmember" &&
						result.Items[0].ID != pending.ID {
						t.Fatal(
							"combined filters returned the wrong deployment",
						)
					}
				})
			}
		})
	}
}
