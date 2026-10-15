//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

type projectToken struct {
	ID         string  `json:"id"`
	TokenID    string  `json:"token_id"`
	Scope      string  `json:"scope"`
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
}

func mintProjectToken(t *testing.T, f *artifactE2E,
	role string, projects ...int64,
) projectToken {
	t.Helper()
	var token projectToken
	body := f.api(t, "POST", "/api/v1/tokens", map[string]any{
		"name": "restricted-" + role, "scope": "scoped",
		"role": role, "project_ids": projects,
	}, 201)
	if err := json.Unmarshal(body, &token); err != nil {
		t.Fatal(err)
	}
	if token.Scope != "scoped" || token.Role != role || token.TokenID == "" ||
		len(token.ProjectIDs) != len(projects) {
		t.Fatal("created token metadata does not match requested grants")
	}
	return token
}

func TestProjectTokensRolesAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := f.token
	projects := []db.Project{f.project, seedProject(t, f.h.repo),
		seedProject(t, f.h.repo)}
	var deployments []db.Deployment
	var releases []db.Release
	for _, project := range projects {
		release := seedRelease(t, f.h.repo, project.ID)
		releases = append(releases, release)
		deployments = append(deployments, seedDeployment(t, f.h.repo,
			release.ID, f.environment.ID, "succeeded"))
	}
	member := seedAPIUser(t, f.h.repo, "token-member@example.test", "deployer")
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		t.Run(role, func(t *testing.T) {
			f.token = owner
			token := mintProjectToken(
				t,
				f,
				role,
				projects[0].ID,
				projects[1].ID,
			)
			f.token = token.ID
			defer func() { f.token = owner }()
			var listed struct {
				Items []struct {
					ID int64 `json:"id"`
				} `json:"items"`
				Total int `json:"total"`
			}
			if err := json.Unmarshal(f.api(
				t,
				"GET",
				"/api/v1/projects?limit=1&offset=1",
				nil,
				200,
			), &listed); err != nil {
				t.Fatal(err)
			}
			if listed.Total != 2 || len(listed.Items) != 1 ||
				listed.Items[0].ID == projects[2].ID {
				t.Fatal("project pagination or count escaped token selection")
			}
			var deploymentsPage struct {
				Items []db.Deployment `json:"items"`
				Total int             `json:"total"`
			}
			if err := json.Unmarshal(f.api(t, "GET", "/api/v1/deployments",
				nil, 200), &deploymentsPage); err != nil {
				t.Fatal(err)
			}
			if deploymentsPage.Total != 2 || len(deploymentsPage.Items) != 2 {
				t.Fatal("deployment rows or totals escaped token selection")
			}
			var activity []struct {
				Counts map[string]int `json:"counts"`
			}
			if err := json.Unmarshal(
				f.api(t, "GET", "/api/v1/deployments/activity",
					nil, 200),
				&activity,
			); err != nil {
				t.Fatal(err)
			}
			var count int
			for _, day := range activity {
				count += day.Counts["succeeded"]
			}
			if count != 2 {
				t.Fatalf("activity count = %d, want 2", count)
			}
			for _, project := range projects[:2] {
				f.api(
					t,
					"GET",
					fmt.Sprintf("/api/v1/projects/%d", project.ID),
					nil,
					200,
				)
			}
			for _, suffix := range []string{"", "/steps", "/variables",
				"/variables/inherited", "/releases", "/runbooks",
				"/runbook-executions", "/runbook-executions/999/logs/stream",
				"/schedules", "/members", "/notifications", "/package-repository"} {
				f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d%s",
					projects[2].ID, suffix), nil, 403)
			}
			for _, suffix := range []string{"", "/status", "/staging",
				"/logs", "/logs/page", "/logs/1", "/logs/stream", "/logs.txt",
				"/events", "/artifact-gates", "/artifact-gates/0/review",
				"/artifact-gates/0/artifact", "/verification"} {
				f.api(t, "GET", fmt.Sprintf("/api/v1/deployments/%d%s",
					deployments[2].ID, suffix), nil, 403)
			}
			f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d/releases/%d",
				projects[0].ID, releases[2].ID), nil, 404)
			for _, path := range []string{"/admin/stats", "/admin/users",
				"/admin/agents", "/admin/audit", "/admin/database-security",
				"/admin/notifications", "/tokens", "/templates"} {
				f.api(t, "GET", "/api/v1"+path, nil, 403)
			}
			for _, method := range []string{"POST", "PATCH", "DELETE"} {
				path := "/api/v1/tokens"
				if method != "POST" {
					path += "/" + token.TokenID
				}
				f.api(t, method, path, map[string]any{"name": "escape",
					"scope": "global"}, 403)
			}
			f.api(
				t,
				"POST",
				"/api/v1/projects",
				map[string]string{"name": "escape"},
				403,
			)
			writeStatus := 201
			if role == "viewer" || role == "deployer" {
				writeStatus = 403
			}
			f.api(
				t,
				"POST",
				fmt.Sprintf("/api/v1/projects/%d/steps", projects[0].ID),
				map[string]string{
					"name":            "token-" + role,
					"script_body":     "echo scoped",
					"container_image": "docker.io/library/bash:5.2",
				},
				writeStatus,
			)
			memberStatus := 403
			if role == "admin" {
				memberStatus = 201
			}
			f.api(
				t,
				"POST",
				fmt.Sprintf("/api/v1/projects/%d/members", projects[0].ID),
				map[string]any{
					"user_id": member.ID,
					"role":    "deployer",
				},
				memberStatus,
			)
			if role != "admin" {
				f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/approve",
					deployments[0].ID), nil, 403)
				f.api(
					t,
					"POST",
					fmt.Sprintf(
						"/api/v1/deployments/%d/artifact-gates/0/approve",
						deployments[0].ID,
					),
					nil,
					403,
				)
			}
			if role == "viewer" {
				for _, action := range []string{"cancel", "retry", "redeploy", "rollback"} {
					f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/%s",
						deployments[0].ID, action), nil, 403)
				}
			}
		})
	}
}

func TestProjectTokensValidationAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for _, grant := range []map[string]any{
		{"scope": "scoped", "role": "viewer", "project_ids": []int64{}},
		{"scope": "scoped", "role": "viewer"},
		{"scope": "scoped", "role": "root", "project_ids": []int64{f.project.ID}},
		{"scope": "scoped", "role": "viewer", "project_ids": []int64{999999}},
		{"scope": "scoped", "role": "viewer", "project_ids": []int64{-1}},
		{"scope": "global", "role": "admin", "project_ids": []int64{f.project.ID}},
		{"scope": "global", "role": "root"},
		{"role": "viewer", "project_ids": []int64{}},
		{"project_ids": nil},
		{"role": nil},
		{"scope": nil},
		{"scope": ""},
	} {
		grant["name"] = "invalid"
		f.api(t, "POST", "/api/v1/tokens", grant, 422)
	}
	var legacy projectToken
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
		map[string]string{"name": "legacy"}, 201), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Scope != "global" {
		t.Fatal("legacy creation changed scope")
	}
	owner := f.token
	f.token = legacy.ID
	f.api(t, "GET", "/api/v1/admin/stats", nil, 200)
	if _, err := f.h.repo.Queries.SetApiTokenScope(
		t.Context(),
		db.SetApiTokenScopeParams{
			ID:    legacy.TokenID,
			Scope: "scoped",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.api(t, "GET", "/api/v1/projects", nil, 401)
	f.token = owner
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID, map[string]any{
		"scope": "scoped", "role": "admin", "project_ids": []int64{},
	}, 422)
	metadata := f.api(t, "GET", "/api/v1/tokens", nil, 200)
	if strings.Contains(string(metadata), token.ID) ||
		strings.Contains(string(metadata), "token_hash") {
		t.Fatal("token metadata exposed bearer credentials")
	}
	f.token = token.ID
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", f.project.ID), nil, 200)
}
