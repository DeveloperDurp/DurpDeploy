package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestGlobalDeploymentList_WebProjectAccess(t *testing.T) {
	for _, role := range []string{"viewer", "deployer", "admin", "nonmember"} {
		t.Run(role, func(t *testing.T) {
			// Given a deployment in each of two projects and distinct environments.
			h := newProjectHarness(t)
			owned, foreign := h.makeProject(
				"owned-list",
			), h.makeProject(
				"foreign-list",
			)
			env, foreignEnv := h.makeEnv("owned-env"), h.makeEnv("foreign-env")
			for _, fixture := range []struct {
				project db.Project
				env     db.Environment
			}{{owned, env}, {foreign, foreignEnv}} {
				release := h.makeRelease(fixture.project.ID, "1.0", "exit 0")
				_, err := h.repo.Queries.CreateDeployment(context.Background(),
					db.CreateDeploymentParams{
						ReleaseID: release.ID, EnvironmentID: fixture.env.ID,
						Status: "pending",
					})
				if err != nil {
					t.Fatal(err)
				}
			}
			userRole := role
			if role == "nonmember" {
				userRole = "deployer"
			}
			if role != "admin" {
				h.setRole(userRole)
			}
			if role != "admin" && role != "nonmember" {
				err := h.repo.Queries.AddProjectMember(context.Background(),
					db.AddProjectMemberParams{
						ProjectID: owned.ID,
						UserID:    h.sess.user.ID,
						Role:      "deployer",
					})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				name, query string
				rows        int
				htmx        bool
			}{
				{"page", "?limit=1", 1, false},
				{"partial", "?limit=1", 1, true},
				{"offset", "?limit=1&offset=1", 0, true},
				{"foreign", fmt.Sprintf("?project_id=%d", foreign.ID), 0, false},
				{"nonexistent", "?project_id=999999", 0, false},
				{"environment", fmt.Sprintf("?env_id=%d", foreignEnv.ID), 0, false},
				{"combined", fmt.Sprintf(
					"?project_id=%d&env_id=%d&status=pending&from=1970-01-01&to=2100-01-01",
					owned.ID, env.ID), 1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// When requesting the page or the HTMX continuation.
					req, err := http.NewRequest(http.MethodGet,
						h.server.URL+"/deployments"+tc.query, nil)
					if err != nil {
						t.Fatal(err)
					}
					if tc.htmx {
						req.Header.Set("HX-Request", "true")
					}
					resp, err := h.authedClient().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					// Then rows, dropdowns, and pagination reveal no foreign data.
					page := string(body)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("status %d: %s", resp.StatusCode, page)
					}
					rows := tc.rows
					if role == "nonmember" {
						rows = 0
					} else if role == "admin" && (tc.name == "offset" ||
						tc.name == "foreign" || tc.name == "environment") {
						rows = 1
					}
					if got := rowCount(page); got != rows {
						t.Fatalf("rows = %d, want %d", got, rows)
					}
					if role != "admin" {
						for _, forbidden := range []string{foreign.Name, foreignEnv.Name, "Load more"} {
							if strings.Contains(page, forbidden) {
								t.Fatalf("page exposes %q", forbidden)
							}
						}
					} else if tc.name == "page" {
						for _, expected := range []string{foreign.Name, foreignEnv.Name, "Load more"} {
							if !strings.Contains(page, expected) {
								t.Fatalf("admin page lacks %q", expected)
							}
						}
					}
					if !tc.htmx && role != "nonmember" &&
						(!strings.Contains(page, owned.Name) || !strings.Contains(page, env.Name)) {
						t.Fatal("authorized dropdown options missing")
					}
				})
			}
		})
	}
}
